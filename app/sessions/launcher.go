package sessions

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// LauncherKeyMode is the new-session launcher's keymap mode: its fields and the dialog.
const LauncherKeyMode = "launcher"

// The launcher's placeholders: what each field asks for.
const (
	PromptPlaceholder = "What should the agent do? A ticket ID goes in the ticket field."
	PRPlaceholder     = "a pull request: https://github.com/owner/repo/pull/123"
	namePlaceholder   = "the new repo's name"
	parentPlaceholder = "where it goes: ~/src"
	dirPlaceholder    = "a directory, opened as it is: ~/src/foo"
)

var (
	tLaunchCard     = theme.Declare("launcher.card", "bg.surface")
	tLaunchCardLine = theme.Declare("launcher.card.line", "line")
	tLaunchCardOn   = theme.DeclareOn("launcher.card.selected", "alpha(accent, 6%)", "bg.surface")
	tLaunchLineOn   = theme.Declare("launcher.card.selected.line", "alpha(accent, 55%)")
	tLaunchRepo     = theme.Declare("launcher.repo", "status.quiet")
)

// launcher is the new-session launcher: prompt, mode, provider, repos and sandbox. Its modes are the
// session host's kinds of session, so a kind a plugin adds there is one more; Start runs session.start.
type Launcher struct {
	kit.Disposer
	h    plugin.Host
	life context.Context // ends when it's deactivated

	open       bool
	focusField bool // focus the mode's first field next frame
	gen        int  // which opening the host's answers are for

	kinds     []core.KindSpec
	providers *plugin.Load[[]host.ProviderInfo]
	loadErr   string
	noAgent   bool // No agent was picked: never by default
	picked    bool // the agent was picked, by the user or once the providers arrived

	kind                                          int
	prompt, ticket, pr, dir, name, parent, branch string
	another                                       string // the ticket Start another was asked for: Start starts one for it
	template                                      string
	provider, model, effort                       string
	chosen                                        []core.RepoID
	repoFilter                                    string
	justAdded                                     core.RepoID // the repo Add repo just brought back ticked
	sandbox, openNet                              bool
	backend                                       string // the sandbox backend picked; "" is sandbox.backend
	busy                                          bool
	err                                           string
	card                                          ticketCard
}

// ticketWait is how long the ticket field rests before its ticket is looked up.
const ticketWait = 350 * time.Millisecond

// ticketCard is the ticket the ticket field names, as its tracker tells it: a ticket ID expands into a
// card. The first registry.TicketTracker that takes the key looks it up.
type ticketCard struct {
	key     string    // what the field held when it last changed
	changed time.Time // when; zero to look it up at once
	asked   string    // the key looked up
	source  string    // who it was asked of: "Linear"
	busy    bool
	since   time.Time // when the lookup started
	t       *registry.Ticket
	err     string
}

func (x *Launcher) Manifest() plugin.Manifest { return plugin.Manifest{ID: "launcher"} }

func (x *Launcher) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.life = h, x.Lifetime()
	has := func(registry.Args) bool { return kit.HasSessions(x.h) }
	x.Add(h.Command(registry.Command{ID: "session.new",
		Title:     "New session: the launcher, with {kind}, {prompt}, {ticket}, {pr}, {dir}, {repos} or {provider} filled in",
		Available: has, Run: func(_ registry.Ctx, a registry.Args) error { x.openWith(a); return nil }}))
	x.Add(h.Command(registry.Command{ID: "session.launch", Title: "Start the session the launcher holds",
		Available: func(registry.Args) bool { return x.open }, Run: func(registry.Ctx, registry.Args) error { return x.start() }}))
	x.Add(h.Command(registry.Command{ID: "launcher.pick-repo", Title: "Tick {repo} in the open launcher",
		Available: func(registry.Args) bool { return x.open }, Run: func(_ registry.Ctx, a registry.Args) error {
			r := core.RepoID(a.String("repo"))
			if r == "" {
				return errors.New("pick which repo? Give its {repo}")
			}
			x.addRepo(r)
			x.justAdded = r
			return nil
		}}))
	x.Add(h.Keymap(registry.Keymap{Mode: LauncherKeyMode, Keys: "<D-CR>", Command: "session.launch", Desc: "Start the session"}))
	x.Add(h.View(kit.OverlayPrefix+"launcher", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "launcher", Fn: x.build}
	})))
	return nil
}

// openWith opens the launcher afresh, with what a command gave filled in, and reads the kinds, providers and repos.
func (x *Launcher) openWith(a registry.Args) {
	spec := kit.SpecFrom(a)
	if x.providers != nil {
		x.providers.Reset()
	}
	*x = Launcher{Disposer: x.Disposer, h: x.h, life: x.life, gen: x.gen + 1, open: true, focusField: true,
		prompt: spec.Prompt, ticket: spec.Ticket, pr: spec.PR, dir: spec.Dir,
		name: spec.Name, template: spec.Template, provider: spec.Provider, model: spec.Model, effort: spec.Effort, chosen: spec.Repos,
		sandbox: spec.Sandbox.Container, openNet: spec.Sandbox.Network == "open", backend: spec.Sandbox.Backend, branch: spec.Branch,
		card: ticketCard{key: strings.TrimSpace(spec.Ticket)}}
	if x.template == "" {
		x.template = host.Templates[0]
	}
	if _, given := a["sandbox"]; !given {
		v, _, _ := x.h.Options().Get("sandbox.default", registry.At{})
		x.sandbox = v == "sandbox"
	}
	x.picked = x.provider != ""
	x.load(core.Kind(a.String("kind")))
}

// load reads the kinds, which are local, at once; the providers' probe runs off the loop.
func (x *Launcher) load(want core.Kind) {
	c, err := kit.SessionHost(x.h)
	if err != nil {
		x.loadErr = err.Error()
		return
	}
	x.kinds = LauncherKinds(registry.Kinds(x.h), want)
	if i := slices.IndexFunc(x.kinds, func(k core.KindSpec) bool { return k.Kind == want }); i >= 0 {
		x.kind = i
	}
	x.providers = plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) ([]host.ProviderInfo, error) {
		ps := c.ProviderInfo(ctx)
		if len(ps) == 0 {
			return nil, errors.New("no agents are set up on this machine")
		}
		return ps, nil
	}, plugin.LoadOpts{Timeout: 20 * time.Second})
	x.providers.Start()
}

// providerList is the providers the probe found; none while it runs.
func (x *Launcher) providerList() []host.ProviderInfo {
	if x.providers == nil {
		return nil
	}
	ps, _ := x.providers.Value()
	return ps
}

// providersState is the probe's state; Ready with none when there's no session host to ask.
func (x *Launcher) providersState() widget.LoadState {
	if x.providers == nil {
		return widget.LoadState{Phase: widget.Ready}
	}
	return x.providers.State()
}

// settle picks the default agent once the providers arrive, unless one was given or picked.
func (x *Launcher) settle() {
	if ps := x.providerList(); !x.picked && len(ps) > 0 {
		x.picked, x.err = true, ""
		provider, model := kit.DefaultAgent(x.h.State(), ps)
		x.pickProvider(provider)
		x.model = model
	}
}

// pickProvider makes id the agent, with its default model.
func (x *Launcher) pickProvider(id string) {
	if x.provider == id && (x.model != "" || id == "") {
		return
	}
	x.provider, x.model, x.effort = id, "", ""
	x.noAgent = id == ""
	for _, p := range x.providerList() {
		if p.ID == id {
			x.model = kit.DefaultModel(p)
		}
	}
}

// LauncherKinds are the kinds the launcher offers: those marked Launcher, and want when it was asked for by name.
func LauncherKinds(all []registry.SessionKind, want core.Kind) []core.KindSpec {
	var out []core.KindSpec
	for _, sk := range all {
		if k := sk.Spec(); k.Launcher || k.Kind == want {
			out = append(out, k)
		}
	}
	return out
}

// pastedPR moves a pull request's link pasted alone into the prompt to the PR field, and picks the kind that starts
// from one.
func (x *Launcher) pastedPR(before string) {
	url := strings.TrimSpace(x.prompt)
	if len([]rune(x.prompt))-len([]rune(before)) < 2 || strings.ContainsAny(url, " \n") {
		return
	}
	if _, ok := kit.SitesOf(x.h).URL(url); !ok {
		return
	}
	i := slices.IndexFunc(x.kinds, func(k core.KindSpec) bool { return startsFrom(k) == core.FromPR })
	if i < 0 {
		return
	}
	x.kind, x.pr, x.prompt, x.focusField, x.err = i, url, "", true, ""
}

func (x *Launcher) current() (core.KindSpec, bool) {
	if x.kind < len(x.kinds) {
		return x.kinds[x.kind], true
	}
	return core.KindSpec{}, false
}

func startsFrom(k core.KindSpec) core.From {
	if k.From == "" {
		return core.FromPrompt
	}
	return k.From
}

// takesRepos says whether the kind starts on repos the launcher picks.
func takesRepos(k core.KindSpec) bool {
	f := startsFrom(k)
	return f == core.FromPrompt || f == core.FromPR
}

// missing is what the session still needs before it can start, or "".
func (x *Launcher) missing() string {
	k, ok := x.current()
	switch {
	case !ok && x.loadErr != "":
		return x.loadErr
	case !ok:
		return "a moment: reading the kinds of session"
	case x.provider == "" && !x.noAgent && x.providersState().Loading():
		return "a moment: checking which agents are installed"
	case x.provider == "" && !x.noAgent:
		return "pick an agent, or No agent"
	case k.NeedsRepo && len(x.chosen) == 0:
		return "pick a repo"
	case startsFrom(k) == core.FromPrompt && x.card.busy:
		return "wait for " + x.card.asked + " from " + x.card.source
	case startsFrom(k) == core.FromPR && strings.TrimSpace(x.pr) == "":
		return "paste the pull request's URL"
	case startsFrom(k) == core.FromName && strings.TrimSpace(x.name) == "":
		return "name the repo"
	case startsFrom(k) == core.FromDir && strings.TrimSpace(x.dir) == "":
		return "pick a directory"
	}
	return ""
}

// home expands a leading ~.
func (x *Launcher) home(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(h, p[1:])
		}
	}
	return p
}

// args are session.start's for what the launcher holds.
func (x *Launcher) args() registry.Args {
	k, _ := x.current()
	a := registry.Args{"kind": string(k.Kind), "prompt": strings.TrimSpace(x.prompt), "provider": x.provider, "model": x.model,
		"effort": x.effort, "sandbox": x.sandbox, "branch": strings.TrimSpace(x.branch)}
	if x.sandbox {
		a["network"] = "ask"
		if x.openNet {
			a["network"] = "open"
		}
		if x.backend != "" {
			a["backend"] = x.backend
		}
	}
	switch startsFrom(k) {
	case core.FromPrompt:
		a["ticket"] = strings.TrimSpace(x.ticket)
		if t := x.ticketHere(); t != nil {
			a["context"] = t.Context
		}
	case core.FromPR:
		a["pr"] = strings.TrimSpace(x.pr)
	case core.FromName:
		a["name"], a["template"], a["dir"] = strings.TrimSpace(x.name), x.template, x.home(x.parent)
	case core.FromDir:
		a["dir"] = x.home(x.dir)
	}
	if takesRepos(k) {
		var repos []string
		for _, r := range x.chosen {
			repos = append(repos, string(r))
		}
		a["repos"] = repos
	}
	if a.String("prompt") != "" || a.String("ticket") != "" {
		a["then"] = []any{map[string]any{"command": "request.open"}} // the first request, as the agent gets it
	}
	return a
}

// start runs session.start with what the launcher holds, or continues the ticket's session; the launcher closes once
// the session is in front.
func (x *Launcher) start() error {
	if x.busy {
		return nil
	}
	if se, ok := x.continues(); ok {
		return x.launch("session.open", registry.Args{"id": string(se.ID())})
	}
	if m := x.missing(); m != "" {
		x.err = "To start: " + m + "."
		return errors.New(x.err)
	}
	return x.launch("session.start", x.args())
}

// launch runs command off the loop, the launcher busy until it answers, and closes the launcher once it has.
func (x *Launcher) launch(command string, a registry.Args) error {
	x.busy, x.err = true, ""
	gen, h, life := x.gen, x.h, x.life
	plugin.Go(h, "launcher: "+command, func() {
		err := plugin.Await(life, func(ctx registry.Ctx) error { return h.Run(ctx, command, a) })
		h.Do(func() {
			if gen != x.gen {
				return
			}
			x.busy = false
			if err != nil {
				x.err = err.Error()
				return
			}
			x.open = false
		})
		h.Redraw()
	})
	return nil
}

// continues is the session the ticket field's ticket already has, when the kind starts from a prompt: Start
// continues it rather than starting another (ticketSession), unless Start another was asked for that ticket.
func (x *Launcher) continues() (core.Session, bool) {
	if k, ok := x.current(); !ok || startsFrom(k) != core.FromPrompt {
		return nil, false
	}
	if t := strings.TrimSpace(x.ticket); t != "" && strings.EqualFold(t, x.another) {
		return nil, false
	}
	return ticketSession(x.h, x.ticket)
}

// continuing is the agent column while Start continues se: the agent se runs on, which Start keeps, and Start
// another, for a new session on the ticket on an agent picked here.
func (x *Launcher) continuing(c *ui.Ctx, se core.Session) {
	th := c.Theme()
	a := se.Snapshot().Agent
	on := "no agent"
	if a.Provider != "" {
		on = kit.ProviderName(x.h, a.Provider)
		if a.Model != "" {
			on += " · " + a.Model
		}
	}
	c.Row("continuing", ui.Style{W: ui.Flex(1), Gap: 14, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
		c.Text("on", se.Name()+" carries on with "+on, kit.MonoFont(11, th.C(theme.FgMuted)))
		if widget.Button(c, "another", "Start another", widget.ButtonOpts{Ghost: true, Help: "agent/launcher#agent"}) {
			x.another = strings.TrimSpace(x.ticket)
		}
	})
}

// saveAsJob hands what the launcher holds to the New job dialog: the prompt, the agent, where it runs.
func (x *Launcher) saveAsJob() {
	a := registry.Args{"prompt": strings.TrimSpace(x.prompt), "provider": x.provider, "model": x.model, "effort": x.effort,
		"sandbox": x.sandbox}
	x.close()
	if err := x.h.Run(context.Background(), "jobs.new", a); err != nil {
		x.h.Log().Error("launcher", "err", err)
	}
}

func (x *Launcher) close() {
	x.open, x.gen, x.another = false, x.gen+1, ""
	if x.providers != nil {
		x.providers.Reset()
	}
}

func (x *Launcher) build(c *ui.Ctx) {
	if !x.open {
		return
	}
	x.settle()
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	escaped := false
	if widget.Scrim(c, "launcher", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: "agent/launcher"}, func(c *ui.Ctx) {
		// Escape on the box, or passed up to it from a control in it (a card, a button).
		for _, k := range c.Interaction(c.Next("box")).Keys {
			escaped = escaped || kit.Notation(k) == "<Esc>"
		}
		c.Column("box", ui.Style{W: ui.Px(min(1260, size.X-32)), H: ui.Px(min(790, size.Y-32)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder),
			Border: 1, Radius: 10, Clip: true, Trap: true, Interactive: true, Focusable: true, KeyMode: LauncherKeyMode,
			Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}}, Role: ui.RoleDialog, Label: "New session", Help: "agent/launcher"},
			func(c *ui.Ctx) {
				x.header(c)
				c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					c.Column("form", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 20, B: 20}, Gap: 22, ScrollY: true}, x.form)
					c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
					c.Column("side", ui.Style{W: ui.Px(330), H: ui.Flex(1), Pad: ui.Pad(16), Gap: 10}, x.side)
				})
				c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				x.footer(c)
			})
	}) || escaped {
		x.close()
	}
}

func (x *Launcher) header(c *ui.Ctx) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Paint("mark", ui.Px(16), ui.Px(16), func(f render.Frame, r render.Rect) {
			cx, cy, h := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()/2-1
			p := render.Path{}
			p.MoveTo(render.Point{X: cx, Y: cy - h}).LineTo(render.Point{X: cx + h, Y: cy}).LineTo(render.Point{X: cx, Y: cy + h}).
				LineTo(render.Point{X: cx - h, Y: cy}).Close()
			f.Path(&p, render.Style{Stroke: th.C(theme.Accent), Width: 1.5})
		})
		c.Text("title", "NEW SESSION", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
			Color: th.C(theme.Fg)})
		meta := []string{}
		if x.ticketHere() != nil {
			meta = append(meta, "from ticket")
		}
		if len(meta) > 0 {
			c.Text("meta", strings.Join(meta, " · "), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: "agent/launcher"}) {
			x.close()
		}
	})
}

// field is a text field of the launcher's: Tab moves on, Escape closes, ⌘↵ starts; lines > 1 makes it a Prose.
func (x *Launcher) field(c *ui.Ctx, key any, text *string, label, placeholder string, first bool, w ui.Size, lines int) {
	id := c.Next(key)
	if first && x.focusField {
		c.RequestFocus(id)
		x.focusField = false
	}
	var r widget.InputResult
	if lines > 1 {
		r = widget.Prose(c, key, text, widget.ProseOpts{Label: label, Placeholder: placeholder, Help: "agent/launcher", TabMoves: true,
			KeyMode: LauncherKeyMode, Width: w, Lines: lines, MinLines: 2})
	} else {
		r = widget.Input(c, key, text, widget.InputOpts{Label: label, Placeholder: placeholder, Help: "agent/launcher", TabMoves: true,
			KeyMode: LauncherKeyMode, Width: w})
	}
	if r.Cancelled {
		x.close()
	}
}

func (x *Launcher) form(c *ui.Ctx) {
	th := c.Theme()
	k, loaded := x.current()
	f := startsFrom(k)
	c.Column("prompt", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		kit.Heading(c, "head", "Prompt", "⌘↵ starts · esc closes")
		switch f {
		case core.FromPR:
			x.field(c, "pr", &x.pr, "Pull request", PRPlaceholder, true, ui.Flex(1), 1)
		case core.FromName:
			c.Row("repo", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "name", &x.name, "Name", namePlaceholder, true, ui.Flex(1), 1)
				if t, ok := widget.Select(c, "template", "template: "+x.template, host.Templates,
					widget.SelectOpts{Label: "Template", Help: "agent/launcher#prompt"}); ok {
					x.template = t
				}
				x.field(c, "parent", &x.parent, "Where it goes", parentPlaceholder, false, ui.Flex(1), 1)
			})
		case core.FromDir:
			x.field(c, "dir", &x.dir, "Directory", dirPlaceholder, true, ui.Flex(1), 1)
		}
		before := x.prompt
		x.field(c, "text", &x.prompt, "Prompt", PromptPlaceholder, f == core.FromPrompt, ui.Flex(1), 8)
		if f == core.FromPrompt {
			x.pastedPR(before)
			x.lookTicket(c)
			x.ticketCard(c)
			c.Row("extra", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "ticket", &x.ticket, "Ticket", "ticket: ENG-2231", false, ui.Px(220), 1)
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				note := "the ticket names the branch and goes to the agent"
				if x.ticketHere() != nil {
					note = "the ticket, comments and attachments go to the agent as context"
				}
				c.Text("note", note, kit.MonoFont(10, th.C(theme.FgFaint)))
			})
		}
	})
	c.Column("mode", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		kit.Heading(c, "head", "Mode", "")
		if !loaded {
			c.Text("wait", cmp.Or(x.loadErr, "No kinds of session"), kit.UIFont(12, th.C(theme.Warn)))
			return
		}
		items := make([]widget.Choice, len(x.kinds))
		for i, k := range x.kinds {
			items[i] = widget.Choice{Title: k.Title, Desc: k.Desc}
		}
		if widget.Choices(c, "kinds", items, &x.kind, widget.ChoicesOpts{Label: "Mode", Help: "agent/launcher#mode"}) {
			x.focusField, x.err = true, ""
		}
	})
	c.Column("agent", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		kit.Heading(c, "head", "Agent", "provider · model · effort")
		if se, ok := x.continues(); ok {
			x.continuing(c, se)
			return
		}
		ps := x.providerList()
		widget.Async(c, "probe", x.providersState(), widget.AsyncOpts{Policy: widget.Gate, Label: "Checking which agents are installed",
			Retry: x.providers.Reload, Help: "agent/launcher#agent", Loading: func(c *ui.Ctx) {
				c.Column("loading", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
					c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
						widget.Skeleton(c, "skeleton", widget.SkeletonOpts{Block: render.Point{X: 260, Y: 96}, Count: 2, Dir: ui.Row, Gap: 10,
							Label: "Agents", Help: "agent/launcher#agent"})
						x.noAgentCard(c, k)
					})
					widget.Busy(c, "busy", x.providersState(), widget.BusyOpts{Label: "Checking which agents are installed", Help: "agent/launcher#agent"})
				})
			}}, func(c *ui.Ctx) {
			c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
				for _, p := range ps {
					x.providerCard(c, p)
				}
				x.noAgentCard(c, k)
			})
		})
		if x.providersState().Phase == widget.Failed {
			c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10, Pad: ui.Insets{T: 10}}, func(c *ui.Ctx) { x.noAgentCard(c, k) })
		}
	})
	c.Row("where", ui.Style{W: ui.Flex(1), Gap: 24}, func(c *ui.Ctx) {
		if takesRepos(k) {
			c.Column("repos", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) { x.reposSection(c, k) })
		}
		c.Column("run", ui.Style{W: ui.Flex(1), Gap: 10}, x.runsIn)
	})
}

// ticketHere is the card's ticket, while the field still names it.
func (x *Launcher) ticketHere() *registry.Ticket {
	if x.card.t == nil || x.card.key != strings.TrimSpace(x.ticket) {
		return nil
	}
	return x.card.t
}

// lookTicket looks up the ticket the field names once it has rested, off the loop.
func (x *Launcher) lookTicket(c *ui.Ctx) {
	key := strings.TrimSpace(x.ticket)
	if key != x.card.key {
		x.card = ticketCard{key: key, changed: c.Now()}
	}
	if key == "" || x.card.asked == key {
		return
	}
	if due := x.card.changed.Add(ticketWait); !x.card.changed.IsZero() && c.Now().Before(due) {
		c.WakeAt(due)
		return
	}
	x.card.asked = key
	src, ok := registry.TrackerFor(x.h, key)
	if !ok {
		return
	}
	x.card.busy, x.card.source, x.card.since = true, src.Label(), c.Now()
	gen, h := x.gen, x.h
	plugin.Go(h, "launcher: ticket "+key, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		t, err := registry.LookupTicket(ctx, src, key)
		h.Do(func() {
			if gen != x.gen || x.card.key != key {
				return
			}
			x.card.busy = false
			if err != nil {
				x.card.err = err.Error()
				return
			}
			x.card.t = &t
		})
		h.Redraw()
	})
}

// ticketCard is the ticket under the prompt: its key and state, title, summary and what's on it; ✕ takes it off.
func (x *Launcher) ticketCard(c *ui.Ctx) {
	const help = "agent/launcher#ticket"
	th := c.Theme()
	t := x.ticketHere()
	se, cont := x.continues()
	switch {
	case t == nil && cont && !x.card.busy:
		c.Text("ticket-continue", continueLabel(se), kit.MonoFont(10.5, th.C(theme.Accent)))
		return
	case x.card.busy:
		widget.Busy(c, "ticket-wait", widget.LoadState{Phase: widget.Pending, Since: x.card.since},
			widget.BusyOpts{Label: "Looking up " + x.card.asked + " in " + x.card.source, Size: 11, Help: help})
		return
	case x.card.err != "" && x.card.key == strings.TrimSpace(x.ticket):
		c.Text("ticket-err", x.card.asked+": "+x.card.err, kit.MonoFont(10.5, th.C(theme.Warn)))
		return
	case t == nil:
		return
	}
	width := float32(min(1260, c.Size().X-32)) - 331 - 48 - 28 - 110 - 14 - 30
	c.Row("ticket-card", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 14, Stroke: th.C(tLaunchCardLine), Border: 1, Radius: 8,
		Role: ui.RoleGroup, Label: "Ticket " + t.Key, Help: help}, func(c *ui.Ctx) {
		c.Column("id", ui.Style{W: ui.Px(110), Gap: 6}, func(c *ui.Ctx) {
			c.Text("key", t.Key, kit.MonoFont(11, th.C(theme.Accent)))
			if t.State != "" {
				widget.Chip(c, "state", t.State, kit.TokenColour(th, stageTone(t.Stage), th.C(theme.Accent)), help)
			}
		})
		c.Column("body", ui.Style{W: ui.Flex(1), Gap: 6, Clip: true}, func(c *ui.Ctx) {
			c.Text("title", t.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			if t.Summary != "" {
				widget.Wrap(c, "summary", t.Summary, width, kit.UIFont(12, th.C(theme.FgMuted)))
			}
			if len(t.Facts) > 0 {
				c.Text("facts", strings.Join(t.Facts, "   "), kit.MonoFont(10, th.C(theme.FgFaint)))
			}
			if cont {
				c.Text("continue", continueLabel(se), kit.MonoFont(10.5, th.C(theme.Accent)))
			}
		})
		if widget.IconButton(c, "drop", "✕", widget.IconButtonOpts{Label: "Take " + t.Key + " off", Help: help, Size: 18}) {
			x.ticket, x.card = "", ticketCard{}
		}
	})
}

// providerCard is one provider: its name, model and effort, and what's left of its plan.
func (x *Launcher) providerCard(c *ui.Ctx, p host.ProviderInfo) {
	th := c.Theme()
	on := x.provider == p.ID
	fill, line := th.C(tLaunchCard), th.C(tLaunchCardLine)
	if on {
		fill, line = th.C(tLaunchCardOn), th.C(tLaunchLineOn)
	}
	name := kit.ProviderName(x.h, p.ID)
	in := widget.Clickable(c, "provider."+p.ID, ui.Style{Dir: ui.Column, W: ui.Flex(1), Pad: ui.Pad(12), Gap: 10, Fill: fill, Stroke: line, Border: 1, Radius: 8,
		NoHover: true, Label: name, Selected: on, Help: "agent/launcher#agent"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("badge", ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 4, Fill: th.C(theme.BgOverlay), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", strings.ToUpper(name[:1]), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold},
					Color: th.C(theme.Fg)})
			})
			c.Text("name", name, kit.UIFont(13, th.C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if id, _ := kit.DefaultAgent(x.h.State(), x.providerList()); p.ID == id {
				widget.Chip(c, "default", "default", th.C(theme.Accent), "agent/launcher#agent")
			}
		})
		if len(p.Models) > 0 {
			model, effort := x.model, ""
			if !on || model == "" {
				model = kit.DefaultModel(p)
			}
			if on {
				effort = x.effort
			}
			if m, e, ok := kit.ModelSelects(c, p, model, effort, false, "agent/launcher#agent"); ok {
				x.pickProvider(p.ID)
				x.model, x.effort = m, e
			}
		}
		c.Text("usage", providerNote(p, x.h.State().Usage), kit.MonoFont(10, th.C(theme.FgFaint)))
	})
	if in.Clicked {
		x.pickProvider(p.ID)
	}
}

// providerNote is a provider's plan and what's used of each window, or why it can't run.
func providerNote(p host.ProviderInfo, usage []core.ProviderUsage) string {
	switch {
	case p.Error != "":
		return p.Error
	case !p.Install.LoggedIn:
		return cmp.Or(p.Install.Note, "not logged in")
	}
	u := p.Usage
	for _, x := range usage {
		if x.Provider == p.ID {
			u = x
		}
	}
	parts := []string{}
	if u.Plan != "" {
		parts = append(parts, u.Plan)
	}
	for _, w := range u.Windows {
		parts = append(parts, fmt.Sprintf("%s %d%%", strings.TrimSuffix(w.Name, " window"), int(w.Used*100+.5)))
	}
	if len(parts) == 0 {
		return cmp.Or(p.Install.Version, "ready")
	}
	return strings.Join(parts, " · ")
}

// noAgentCard starts the session with no agent.
func (x *Launcher) noAgentCard(c *ui.Ctx, k core.KindSpec) {
	th := c.Theme()
	on := x.provider == "" && x.noAgent
	fill, line := render.Color{}, th.C(tLaunchCardLine)
	if on {
		fill, line = th.C(tLaunchCardOn), th.C(tLaunchLineOn)
	}
	note := "you and the editor"
	if k.InPlace() {
		note = "an agent would edit your real files"
	}
	if widget.Clickable(c, "provider.none", ui.Style{Dir: ui.Column, W: ui.Px(200), Pad: ui.Pad(12), Gap: 8, Clip: true, Fill: fill, Stroke: line, Border: 1, Radius: 8,
		NoHover: true, Label: "No agent", Selected: on, Help: "agent/launcher#agent"}, func(c *ui.Ctx) {
		c.Text("name", "No agent", kit.UIFont(13, th.C(theme.Fg)))
		c.Text("note", note, kit.MonoFont(10, th.C(theme.FgFaint)))
	}).Clicked {
		x.picked = true
		x.pickProvider("")
	}
}

// runsIn is where the session runs: in a sandbox or not, which backend, and its network.
func (x *Launcher) runsIn(c *ui.Ctx) {
	kit.Heading(c, "head", "Runs in", "")
	pick := kit.BackendPick{Chosen: &x.backend, Setting: kit.SettingBackend(x.h), Offered: x.h.State().Machine.Sandboxes}
	kit.SandboxToggles(c, &x.sandbox, &x.openNet, pick, "agent/launcher#runs-in")
}

// side is the recent prompts, and what starting will do.
func (x *Launcher) side(c *ui.Ctx) {
	th := c.Theme()
	kit.Heading(c, "head", "Recent prompts", "")
	sessions := slices.Clone(x.h.State().Sessions)
	sessions = slices.DeleteFunc(sessions, func(se core.SessionState) bool { return strings.TrimSpace(se.Prompt) == "" })
	slices.SortStableFunc(sessions, func(a, b core.SessionState) int { return b.Created.Compare(a.Created) })
	c.Column("recent", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		if len(sessions) == 0 {
			c.Text("none", "Prompts you start sessions with show here.", kit.UIFont(12, th.C(theme.FgFaint)))
		}
		for _, se := range sessions[:min(len(sessions), 5)] {
			meta := []widget.AgoPart{{Text: string(se.Kind)}}
			for _, p := range se.Projects {
				meta = append(meta, widget.AgoPart{Text: p.Name})
			}
			meta = append(meta, widget.AgoPart{At: se.Created})
			if widget.Clickable(c, "recent."+string(se.ID), ui.Style{Dir: ui.Column, W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 4, Fill: th.C(tLaunchCard),
				Stroke: th.C(tLaunchCardLine), Border: 1, Radius: 8, Clip: true, Label: kit.FirstLine(se.Prompt), Help: "agent/launcher#recent-prompts"},
				func(c *ui.Ctx) {
					c.Text("p", kit.FirstLine(se.Prompt), kit.UIFont(12.5, th.C(theme.Fg)))
					widget.AgoLine(c, "m", widget.AgoLineOpts{Text: kit.MonoFont(10, th.C(theme.FgFaint))}, meta...)
				}).Clicked {
				x.reuse(se)
			}
		}
	})
	c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
	c.Column("preflight", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 6, Stroke: th.C(tLaunchCardLine), Border: 1, Radius: 8,
		Role: ui.RoleGroup, Label: "Pre-flight"}, func(c *ui.Ctx) {
		for i, row := range x.preflight() {
			c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				c.Text("k", row[0], kit.MonoFont(10.5, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("v", row[1], kit.MonoFont(10.5, th.C(theme.Fg)))
			})
		}
	})
}

// reuse fills the launcher from an earlier session: its prompt, kind and repos.
func (x *Launcher) reuse(se core.SessionState) {
	x.prompt, x.ticket, x.chosen = se.Prompt, se.Ticket, nil
	if i := slices.IndexFunc(x.kinds, func(k core.KindSpec) bool { return k.Kind == se.Kind }); i >= 0 {
		x.kind = i
	}
	for _, p := range se.Projects {
		x.addRepo(core.RepoID(cmp.Or(p.Repo, p.Dir)))
	}
	x.focusField = true
}

// preflight is what starting will do: the worktrees, the repos to fetch, the agent, where it runs.
func (x *Launcher) preflight() [][2]string {
	k, ok := x.current()
	if !ok {
		return [][2]string{{"kinds", "none"}}
	}
	worktrees := map[core.Worktrees]string{core.WorktreesNow: "now, from a fetched base", core.WorktreesStartWork: "at Start work",
		core.WorktreesPR: "the PR's head, read-only", core.WorktreesInit: "a new repo",
		core.WorktreesNone: "none: the directory itself"}[k.Worktrees]
	rows := [][2]string{{"worktrees", worktrees}}
	if takesRepos(k) && k.Worktrees != core.WorktreesStartWork {
		rows = append(rows, [2]string{"repos to fetch", fmt.Sprint(len(x.chosen))})
	}
	agent := "none"
	switch {
	case x.provider != "":
		agent = strings.TrimSpace(kit.ProviderName(x.h, x.provider) + " " + x.model)
	case !x.noAgent && x.providersState().Loading():
		agent = "waiting on the probe"
	case !x.noAgent:
		agent = "not picked"
	}
	rows = append(rows, [2]string{"agent", agent}, [2]string{"prompt pack", cmp.Or(k.Pack, "none")})
	runs, network := "on this machine", "this machine's"
	if x.sandbox {
		runs, network = "in a "+kit.SandboxNoun(kit.BackendPick{Chosen: &x.backend, Setting: kit.SettingBackend(x.h)}.Current()), "ask per new host"
		if x.openNet {
			network = "open"
		}
	}
	return append(rows, [2]string{"agent runs", runs}, [2]string{"network", network})
}

func (x *Launcher) footer(c *ui.Ctx) {
	th := c.Theme()
	k, _ := x.current()
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Column("about", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			if x.err != "" {
				c.Text("err", x.err, kit.MonoFont(11, th.C(theme.Error)))
				return
			}
			c.Text("t", k.About, kit.MonoFont(11, th.C(theme.FgMuted)))
		})
		if widget.Button(c, "savejob", "Save as job", widget.ButtonOpts{Ghost: true, Disabled: !x.h.Available("jobs.new", nil),
			Help: "agent/launcher#save-as-job"}) {
			x.saveAsJob()
		}
		label := "Start"
		if k.Title != "" {
			label = "Start " + k.Title
		}
		if _, ok := x.continues(); ok {
			label = "Continue"
		}
		probing := x.provider == "" && !x.noAgent && x.providersState().Loading()
		busyLabel := "Starting"
		if probing {
			busyLabel = "Checking agents"
		}
		if widget.Button(c, "start", label, widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: k.Kind == "" || x.provider == "" && !x.noAgent, Busy: x.busy || probing,
			BusyLabel: busyLabel, Help: "agent/launcher"}) {
			_ = x.start()
		}
	})
}
