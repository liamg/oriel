package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
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
	promptPlaceholder = "What should the agent do? A ticket ID goes in the ticket field."
	prPlaceholder     = "a pull request: https://github.com/owner/repo/pull/123"
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

// launcher is the new-session launcher (ui.md 04): prompt, mode, provider, repos and sandbox. Its modes are the
// session host's kinds of session, so a kind a plugin adds there is one more; Start runs session.start.
type launcher struct {
	disposer
	sh *shell
	h  plugin.Host

	open       bool
	focusField bool // focus the mode's first field next frame
	gen        int  // which opening the host's answers are for

	kinds     []host.KindSpec
	providers *plugin.Load[[]host.ProviderInfo]
	loadErr   string
	noAgent   bool // No agent was picked: never by default
	picked    bool // the agent was picked, by the user or once the providers arrived

	kind                                          int
	prompt, ticket, pr, dir, name, parent, branch string
	template, variant                             string
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

// ticketCard is the ticket the ticket field names, as its tracker tells it (ui.md 04: a ticket ID expands into a
// card). The first registry.TicketTracker that takes the key looks it up.
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

func (x *launcher) Manifest() plugin.Manifest { return plugin.Manifest{ID: "launcher"} }

func (x *launcher) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	has := func(registry.Args) bool { return x.sh.sessions != nil && x.sh.cfg.Host != nil }
	x.add(h.Command(registry.Command{ID: "session.new",
		Title:     "New session: the launcher, with {kind}, {prompt}, {ticket}, {pr}, {dir}, {repos} or {provider} filled in",
		Available: has, Run: func(_ registry.Ctx, a registry.Args) error { x.openWith(a); return nil }}))
	x.add(h.Command(registry.Command{ID: "session.launch", Title: "Start the session the launcher holds",
		Available: func(registry.Args) bool { return x.open }, Run: func(registry.Ctx, registry.Args) error { return x.start() }}))
	x.add(h.Command(registry.Command{ID: "launcher.pick-repo", Title: "Tick {repo} in the open launcher",
		Available: func(registry.Args) bool { return x.open }, Run: func(_ registry.Ctx, a registry.Args) error {
			r := core.RepoID(a.String("repo"))
			if r == "" {
				return errors.New("pick which repo? Give its {repo}")
			}
			x.addRepo(r)
			x.justAdded = r
			return nil
		}}))
	x.add(h.Keymap(registry.Keymap{Mode: LauncherKeyMode, Keys: "<D-CR>", Command: "session.launch", Desc: "Start the session"}))
	x.add(h.View(OverlayPrefix+"launcher", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "launcher", Fn: x.build}
	})))
	return nil
}

// openWith opens the launcher afresh, with what a command gave filled in, and reads the kinds, providers and repos.
func (x *launcher) openWith(a registry.Args) {
	spec := specFrom(a)
	if x.providers != nil {
		x.providers.Reset()
	}
	*x = launcher{disposer: x.disposer, sh: x.sh, h: x.h, gen: x.gen + 1, open: true, focusField: true,
		prompt: spec.Prompt, ticket: spec.Ticket, pr: spec.PR, dir: spec.Dir,
		name: spec.Name, template: spec.Template, variant: spec.Variant, provider: spec.Provider, model: spec.Model, effort: spec.Effort, chosen: spec.Repos,
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
func (x *launcher) load(want core.Kind) {
	sessions := x.sh.sessions
	if sessions == nil {
		x.loadErr = "there are no sessions in this window"
		return
	}
	c, err := sessions.host()
	if err != nil {
		x.loadErr = err.Error()
		return
	}
	x.kinds = c.Kinds().All()
	if i := slices.IndexFunc(x.kinds, func(k host.KindSpec) bool { return k.Kind == want }); i >= 0 {
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
func (x *launcher) providerList() []host.ProviderInfo {
	if x.providers == nil {
		return nil
	}
	ps, _ := x.providers.Value()
	return ps
}

// providersState is the probe's state; Ready with none when there's no session host to ask.
func (x *launcher) providersState() widget.LoadState {
	if x.providers == nil {
		return widget.LoadState{Phase: widget.Ready}
	}
	return x.providers.State()
}

// settle picks the default agent once the providers arrive, unless one was given or picked.
func (x *launcher) settle() {
	if ps := x.providerList(); !x.picked && len(ps) > 0 {
		x.picked, x.err = true, ""
		x.pickProvider(defaultProvider(ps))
	}
}

// defaultProvider is the first provider that's logged in, else the first.
func defaultProvider(ps []host.ProviderInfo) string {
	for _, p := range ps {
		if p.Install.LoggedIn && p.Error == "" {
			return p.ID
		}
	}
	if len(ps) > 0 {
		return ps[0].ID
	}
	return ""
}

// pickProvider makes id the agent, with its default model.
func (x *launcher) pickProvider(id string) {
	if x.provider == id && (x.model != "" || id == "") {
		return
	}
	x.provider, x.model, x.effort = id, "", ""
	x.noAgent = id == ""
	for _, p := range x.providerList() {
		if p.ID == id {
			x.model = defaultModel(p)
		}
	}
}

func (x *launcher) current() (host.KindSpec, bool) {
	if x.kind < len(x.kinds) {
		return x.kinds[x.kind], true
	}
	return host.KindSpec{}, false
}

func startsFrom(k host.KindSpec) host.From {
	if k.From == "" {
		return host.FromPrompt
	}
	return k.From
}

// takesRepos says whether the kind starts on repos the launcher picks.
func takesRepos(k host.KindSpec) bool {
	f := startsFrom(k)
	return f == host.FromPrompt || f == host.FromPR
}

// missing is what the session still needs before it can start, or "".
func (x *launcher) missing() string {
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
	case startsFrom(k) == host.FromPrompt && x.card.busy:
		return "wait for " + x.card.asked + " from " + x.card.source
	case startsFrom(k) == host.FromPR && strings.TrimSpace(x.pr) == "":
		return "paste the pull request's URL"
	case startsFrom(k) == host.FromName && strings.TrimSpace(x.name) == "":
		return "name the repo"
	case startsFrom(k) == host.FromDir && strings.TrimSpace(x.dir) == "":
		return "pick a directory"
	}
	return ""
}

// chosenVariant is the kind's variant the launcher holds: the one picked, else its first; "" for a kind with none.
func (x *launcher) chosenVariant(k host.KindSpec) string {
	for _, v := range k.Variants {
		if v.ID == x.variant {
			return v.ID
		}
	}
	if len(k.Variants) > 0 {
		return k.Variants[0].ID
	}
	return ""
}

// variantField picks the kind's variant (a tour's walkthrough, demo…).
func (x *launcher) variantField(c *ui.Ctx, k host.KindSpec) {
	cur := x.chosenVariant(k)
	var opts []string
	label := cur
	for _, v := range k.Variants {
		o := v.ID + " · " + v.Desc
		opts = append(opts, o)
		if v.ID == cur {
			label = o
		}
	}
	if o, ok := widget.Select(c, "variant", strings.ToLower(k.Title)+": "+label, opts, widget.SelectOpts{Label: k.Title + " kind", Help: "agent/launcher#prompt",
		Width: 300}); ok {
		x.variant, _, _ = strings.Cut(o, " · ")
	}
}

// home expands a leading ~.
func (x *launcher) home(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, "~") {
		if h, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(h, p[1:])
		}
	}
	return p
}

// args are session.start's for what the launcher holds.
func (x *launcher) args() registry.Args {
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
	if v := x.chosenVariant(k); v != "" {
		a["variant"] = v
	}
	switch startsFrom(k) {
	case host.FromPrompt:
		a["ticket"] = strings.TrimSpace(x.ticket)
		if t := x.ticketHere(); t != nil {
			a["context"] = t.Context
		}
	case host.FromPR:
		a["pr"] = strings.TrimSpace(x.pr)
	case host.FromName:
		a["name"], a["template"], a["dir"] = strings.TrimSpace(x.name), x.template, x.home(x.parent)
	case host.FromDir:
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
func (x *launcher) start() error {
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
func (x *launcher) launch(command string, a registry.Args) error {
	x.busy, x.err = true, ""
	gen, h := x.gen, x.h
	go func() {
		err := h.Run(context.Background(), command, a)
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
	}()
	return nil
}

// continues is the session the ticket field's ticket already has, when the kind starts from a prompt: Start
// continues it rather than starting another (ticketSession).
func (x *launcher) continues() (core.Session, bool) {
	if k, ok := x.current(); !ok || startsFrom(k) != host.FromPrompt {
		return nil, false
	}
	return ticketSession(x.h, x.ticket)
}

// saveAsJob hands what the launcher holds to the New job dialog: the prompt, the agent, where it runs.
func (x *launcher) saveAsJob() {
	a := registry.Args{"prompt": strings.TrimSpace(x.prompt), "provider": x.provider, "model": x.model, "effort": x.effort,
		"sandbox": x.sandbox}
	x.close()
	if err := x.h.Run(context.Background(), "jobs.new", a); err != nil {
		x.h.Log().Error("launcher", "err", err)
	}
}

func (x *launcher) close() {
	x.open, x.gen = false, x.gen+1
	if x.providers != nil {
		x.providers.Reset()
	}
}

func (x *launcher) build(c *ui.Ctx) {
	if !x.open {
		return
	}
	x.settle()
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	box := c.Next("box")
	for _, k := range c.Interaction(box).Keys {
		if notation(k) == "<Esc>" {
			x.close()
			return
		}
	}
	if widget.Scrim(c, "launcher", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: "agent/launcher"}, func(c *ui.Ctx) {
		c.Column("box", ui.Style{W: ui.Px(min(1260, size.X-32)), H: ui.Px(min(790, size.Y-32)), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder),
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
	}) {
		x.close()
	}
}

func (x *launcher) header(c *ui.Ctx) {
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
			c.Text("meta", strings.Join(meta, " · "), monoFont(10.5, th.C(theme.FgFaint)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: "agent/launcher"}) {
			x.close()
		}
	})
}

// heading is a section's caption, with a note at its right.
func heading(c *ui.Ctx, key any, title, note string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Pad: ui.Insets{B: 8}}, func(c *ui.Ctx) {
		caption(c, "cap", title)
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if note != "" {
			c.Text("note", note, monoFont(10, th.C(theme.FgFaint)))
		}
	})
}

// field is a text field of the launcher's: Tab moves on, Escape closes, ⌘↵ starts; lines > 1 takes several lines.
func (x *launcher) field(c *ui.Ctx, key any, text *string, label, placeholder string, first bool, w ui.Size, lines int) {
	id := c.Next(key)
	if first && x.focusField {
		c.Focus(id)
		x.focusField = false
	}
	r := widget.Input(c, key, text, widget.InputOpts{Label: label, Placeholder: placeholder, Help: "agent/launcher", TabMoves: true,
		KeyMode: LauncherKeyMode, Width: w, Lines: lines, MinLines: 2})
	if r.Cancelled {
		x.close()
	}
}

func (x *launcher) addRepo(r core.RepoID) {
	if r != "" && !slices.Contains(x.chosen, r) {
		x.chosen = append(x.chosen, r)
	}
}

func (x *launcher) form(c *ui.Ctx) {
	th := c.Theme()
	k, loaded := x.current()
	f := startsFrom(k)
	c.Column("prompt", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		heading(c, "head", "Prompt", "⌘↵ starts · esc closes")
		switch f {
		case host.FromPR:
			x.field(c, "pr", &x.pr, "Pull request", prPlaceholder, true, ui.Flex(1), 1)
		case host.FromName:
			c.Row("repo", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "name", &x.name, "Name", namePlaceholder, true, ui.Flex(1), 1)
				if t, ok := widget.Select(c, "template", "template: "+x.template, host.Templates,
					widget.SelectOpts{Label: "Template", Help: "agent/launcher#prompt"}); ok {
					x.template = t
				}
				x.field(c, "parent", &x.parent, "Where it goes", parentPlaceholder, false, ui.Flex(1), 1)
			})
		case host.FromDir:
			x.field(c, "dir", &x.dir, "Directory", dirPlaceholder, true, ui.Flex(1), 1)
		}
		x.field(c, "text", &x.prompt, "Prompt", promptPlaceholder, f == host.FromPrompt || f == host.FromNothing, ui.Flex(1), 8)
		if f == host.FromPrompt {
			x.lookTicket(c)
			x.ticketCard(c)
			c.Row("extra", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "ticket", &x.ticket, "Ticket", "ticket: ENG-2231", false, ui.Px(220), 1)
				if len(k.Variants) > 0 {
					x.variantField(c, k)
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				note := "the ticket names the branch and goes to the agent"
				if x.ticketHere() != nil {
					note = "the ticket, comments and attachments go to the agent as context"
				}
				c.Text("note", note, monoFont(10, th.C(theme.FgFaint)))
			})
		}
	})
	c.Column("mode", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		heading(c, "head", "Mode", "")
		if !loaded {
			c.Text("wait", cmpOr(x.loadErr, "No kinds of session"), uiFont(12, th.C(theme.Warn)))
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
		heading(c, "head", "Agent", "provider · model · effort")
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
func (x *launcher) ticketHere() *registry.Ticket {
	if x.card.t == nil || x.card.key != strings.TrimSpace(x.ticket) {
		return nil
	}
	return x.card.t
}

// lookTicket looks up the ticket the field names once it has rested, off the loop.
func (x *launcher) lookTicket(c *ui.Ctx) {
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
	go func() {
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
	}()
}

// ticketCard is the ticket under the prompt: its key and state, title, summary and what's on it; ✕ takes it off.
func (x *launcher) ticketCard(c *ui.Ctx) {
	const help = "agent/launcher#ticket"
	th := c.Theme()
	t := x.ticketHere()
	se, cont := x.continues()
	switch {
	case t == nil && cont && !x.card.busy:
		c.Text("ticket-continue", continueLabel(se), monoFont(10.5, th.C(theme.Accent)))
		return
	case x.card.busy:
		widget.Busy(c, "ticket-wait", widget.LoadState{Phase: widget.Pending, Since: x.card.since},
			widget.BusyOpts{Label: "Looking up " + x.card.asked + " in " + x.card.source, Size: 11, Help: help})
		return
	case x.card.err != "" && x.card.key == strings.TrimSpace(x.ticket):
		c.Text("ticket-err", x.card.asked+": "+x.card.err, monoFont(10.5, th.C(theme.Warn)))
		return
	case t == nil:
		return
	}
	width := float32(min(1260, c.Size().X-32)) - 331 - 48 - 28 - 110 - 14 - 30
	c.Row("ticket-card", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 14, Stroke: th.C(tLaunchCardLine), Border: 1, Radius: 8,
		Role: ui.RoleGroup, Label: "Ticket " + t.Key, Help: help}, func(c *ui.Ctx) {
		c.Column("id", ui.Style{W: ui.Px(110), Gap: 6}, func(c *ui.Ctx) {
			c.Text("key", t.Key, monoFont(11, th.C(theme.Accent)))
			if t.State != "" {
				widget.Chip(c, "state", t.State, tokenColour(th, stageTone(t.Stage), th.C(theme.Accent)), help)
			}
		})
		c.Column("body", ui.Style{W: ui.Flex(1), Gap: 6, Clip: true}, func(c *ui.Ctx) {
			c.Text("title", t.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			if t.Summary != "" {
				widget.Wrap(c, "summary", t.Summary, width, uiFont(12, th.C(theme.FgMuted)))
			}
			if len(t.Facts) > 0 {
				c.Text("facts", strings.Join(t.Facts, "   "), monoFont(10, th.C(theme.FgFaint)))
			}
			if cont {
				c.Text("continue", continueLabel(se), monoFont(10.5, th.C(theme.Accent)))
			}
		})
		if widget.IconButton(c, "drop", "✕", widget.IconButtonOpts{Label: "Take " + t.Key + " off", Help: help, Size: 18}) {
			x.ticket, x.card = "", ticketCard{}
		}
	})
}

// providerCard is one provider: its name, model and effort, and what's left of its plan.
func (x *launcher) providerCard(c *ui.Ctx, p host.ProviderInfo) {
	th := c.Theme()
	on := x.provider == p.ID
	fill, line := th.C(tLaunchCard), th.C(tLaunchCardLine)
	if on {
		fill, line = th.C(tLaunchCardOn), th.C(tLaunchLineOn)
	}
	name := providerName(p.ID)
	in := widget.Clickable(c, "provider."+p.ID, ui.Style{Dir: ui.Column, W: ui.Flex(1), Pad: ui.Pad(12), Gap: 10, Fill: fill, Stroke: line, Border: 1, Radius: 8,
		NoHover: true, Label: name, Selected: on, Help: "agent/launcher#agent"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("badge", ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 4, Fill: th.C(theme.BgOverlay), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", strings.ToUpper(name[:1]), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold},
					Color: th.C(theme.Fg)})
			})
			c.Text("name", name, uiFont(13, th.C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if p.ID == defaultProvider(x.providerList()) {
				widget.Chip(c, "default", "default", th.C(theme.Accent), "agent/launcher#agent")
			}
		})
		if len(p.Models) > 0 {
			model, effort := x.model, ""
			if !on || model == "" {
				model = defaultModel(p)
			}
			if on {
				effort = x.effort
			}
			if m, e, ok := modelSelects(c, p, model, effort, "agent/launcher#agent"); ok {
				x.pickProvider(p.ID)
				x.model, x.effort = m, e
			}
		}
		c.Text("usage", providerNote(p, x.h.State().Usage), monoFont(10, th.C(theme.FgFaint)))
	})
	if in.Clicked {
		x.pickProvider(p.ID)
	}
}

// modelSelects are a provider's model and effort menus, at model and effort ("" is the default effort). It reports
// a pick, with what's picked: a new model's effort is its default.
func modelSelects(c *ui.Ctx, p host.ProviderInfo, model, effort, help string) (string, string, bool) {
	name := providerName(p.ID)
	picked := false
	c.Row("model", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		var ids, efforts []string
		for _, m := range p.Models {
			ids = append(ids, m.ID)
			if m.ID == model {
				efforts = m.Efforts
			}
		}
		if v, ok := widget.Select(c, "m", model, ids, widget.SelectOpts{Label: name + " model", Help: help}); ok {
			model, effort, picked = v, "", true
		}
		if len(efforts) > 0 {
			label := cmpOr(effort, "default effort")
			if v, ok := widget.Select(c, "e", label, efforts, widget.SelectOpts{Label: name + " effort", Help: help}); ok {
				effort, picked = v, true
			}
		}
	})
	return model, effort, picked
}

// defaultModel is a provider's default model, else its first.
func defaultModel(p host.ProviderInfo) string {
	for _, m := range p.Models {
		if m.Default {
			return m.ID
		}
	}
	if len(p.Models) > 0 {
		return p.Models[0].ID
	}
	return ""
}

// providerNote is a provider's plan and what's used of each window, or why it can't run.
func providerNote(p host.ProviderInfo, usage []core.ProviderUsage) string {
	switch {
	case p.Error != "":
		return p.Error
	case !p.Install.LoggedIn:
		return cmpOr(p.Install.Note, "not logged in")
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
		return cmpOr(p.Install.Version, "ready")
	}
	return strings.Join(parts, " · ")
}

// noAgentCard starts the session with no agent.
func (x *launcher) noAgentCard(c *ui.Ctx, k host.KindSpec) {
	th := c.Theme()
	on := x.provider == "" && x.noAgent
	fill, line := render.Color{}, th.C(tLaunchCardLine)
	if on {
		fill, line = th.C(tLaunchCardOn), th.C(tLaunchLineOn)
	}
	note := "you and the editor"
	if k.Kind == core.KindDirect {
		note = "an agent would edit your real files"
	}
	if widget.Clickable(c, "provider.none", ui.Style{Dir: ui.Column, W: ui.Px(200), Pad: ui.Pad(12), Gap: 8, Clip: true, Fill: fill, Stroke: line, Border: 1, Radius: 8,
		NoHover: true, Label: "No agent", Selected: on, Help: "agent/launcher#agent"}, func(c *ui.Ctx) {
		c.Text("name", "No agent", uiFont(13, th.C(theme.Fg)))
		c.Text("note", note, monoFont(10, th.C(theme.FgFaint)))
	}).Clicked {
		x.picked = true
		x.pickProvider("")
	}
}

// repoRowsShown is how many repo rows the launcher shows before its list scrolls; repoFilterAfter, how many repos
// there are before it has a filter.
const (
	repoRowsShown   = 6
	repoFilterAfter = 8
	repoRowHeight   = 28
)

// repoChoices are the repos the launcher lists: ticked first, then the most recently used, then by name. A path
// ticked that isn't registered is listed as it is.
func (x *launcher) repoChoices() []core.Repo {
	repos := slices.Clone(x.h.State().Repos)
	for _, id := range x.chosen {
		if !slices.ContainsFunc(repos, func(r core.Repo) bool { return r.ID == id || r.Path == string(id) }) {
			repos = append(repos, core.Repo{ID: id, Path: string(id)})
		}
	}
	ticked := func(r core.Repo) bool { return x.ticked(r) }
	slices.SortStableFunc(repos, func(a, b core.Repo) int {
		if ta, tb := ticked(a), ticked(b); ta != tb {
			if ta {
				return -1
			}
			return 1
		}
		if c := b.Used.Compare(a.Used); c != 0 {
			return c
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return repos
}

func (x *launcher) ticked(r core.Repo) bool {
	return slices.ContainsFunc(x.chosen, func(id core.RepoID) bool { return id == r.ID || string(id) == r.Path })
}

func (x *launcher) toggleRepo(r core.Repo) {
	if x.ticked(r) {
		x.chosen = slices.DeleteFunc(x.chosen, func(id core.RepoID) bool { return id == r.ID || string(id) == r.Path })
		return
	}
	x.addRepo(r.ID)
}

func (x *launcher) reposSection(c *ui.Ctx, k host.KindSpec) {
	th := c.Theme()
	about := "one worktree each · same branch"
	if startsFrom(k) == host.FromPR {
		about = "the PR's repo, found by its remote if none"
	}
	heading(c, "head", "Repositories", about)
	all := x.repoChoices()
	addNew := func(c *ui.Ctx) {
		if widget.Button(c, "addrepo", "+ Add repo", widget.ButtonOpts{Help: "agent/launcher#repositories"}) {
			if err := x.h.Run(context.Background(), "repos.new", registry.Args{"then": []any{map[string]any{"command": "launcher.pick-repo"}}}); err != nil {
				x.err = err.Error()
			}
		}
	}
	if len(all) == 0 {
		c.Row("none", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "No repos yet.", uiFont(12.5, th.C(theme.FgFaint)))
			addNew(c)
		})
	} else {
		c.Row("tools", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			if len(all) > repoFilterAfter {
				if r := widget.Input(c, "repofilter", &x.repoFilter, widget.InputOpts{Label: "Filter repos", Placeholder: "⌕ filter repos",
					Help: "agent/launcher#repositories", KeyMode: LauncherKeyMode, TabMoves: true}); r.Cancelled {
					x.repoFilter = ""
				}
			} else {
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			}
			addNew(c)
		})
		q := strings.ToLower(strings.TrimSpace(x.repoFilter))
		shown := slices.DeleteFunc(slices.Clone(all), func(r core.Repo) bool {
			return q != "" && !x.ticked(r) && !strings.Contains(strings.ToLower(string(r.ID)+" "+r.Path), q)
		})
		h := ui.Size{}
		if len(shown) > repoRowsShown {
			h = ui.Px(repoRowsShown * repoRowHeight)
		}
		c.Column("repolist", ui.Style{W: ui.Flex(1), H: h, ScrollY: len(shown) > repoRowsShown, Stroke: th.C(theme.Line), Border: 1, Radius: 6,
			Clip: true, Role: ui.RoleList, Label: "Repositories"}, func(c *ui.Ctx) {
			for _, r := range shown {
				x.repoRow(c, r)
			}
			if len(shown) == 0 {
				c.Row("nomatch", ui.Style{W: ui.Flex(1), H: ui.Px(repoRowHeight), Pad: ui.PadXY(10, 0), Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("t", "No repo matches.", uiFont(12, th.C(theme.FgFaint)))
				})
			}
		})
		if n := len(x.chosen); n > 0 {
			note(c, "selected", fmt.Sprintf("%d selected", n))
		}
	}
	if k.Worktrees == host.WorktreesNow || k.Worktrees == host.WorktreesStartWork {
		c.Row("branch", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("l", "Branch", uiFont(12.5, th.C(theme.FgMuted)))
			x.field(c, "branch", &x.branch, "Branch", "named from the ticket and prompt", false, ui.Flex(1), 1)
		})
	}
}

// repoRow is a repo in the launcher's checklist: its box and name, path, default branch, and when it was last used.
func (x *launcher) repoRow(c *ui.Ctx, r core.Repo) {
	th := c.Theme()
	on := x.ticked(r)
	fill := render.Color{}
	if on {
		fill = th.C(tLaunchCardOn)
	}
	c.Row("r."+string(r.ID), ui.Style{W: ui.Flex(1), H: ui.Px(repoRowHeight), Pad: ui.PadXY(10, 0), Gap: 12, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
		st := widget.CheckOff
		if on {
			st = widget.CheckOn
		}
		c.Row("name", ui.Style{W: ui.Px(170), Clip: true}, func(c *ui.Ctx) {
			if widget.Check(c, "check", st, widget.CheckOpts{Text: string(r.ID), Plain: true, Label: string(r.ID), Help: "agent/launcher#repositories"}) {
				x.toggleRepo(r)
			}
		})
		c.Row("path", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", tildePath(r.Path), monoFont(11, th.C(theme.FgFaint)))
		})
		c.Text("branch", strings.TrimPrefix(r.BaseBranch, "origin/"), monoFont(10.5, th.C(theme.FgFaint)))
		c.Row("when", ui.Style{W: ui.Px(110), Main: ui.End}, func(c *ui.Ctx) {
			switch {
			case r.ID == x.justAdded:
				widget.Chip(c, "chip", "just added", th.C(theme.OK), "agent/launcher#repositories")
			case isFound(r):
				widget.Chip(c, "chip", "found", th.C(tRepoFound), "agent/launcher#repositories")
			case !r.Used.IsZero():
				c.Text("t", "used "+agoText(time.Now(), r.Used), monoFont(10, th.C(theme.FgGhost)))
			}
		})
	})
}

// runsIn is where the session runs: in a sandbox or not, which backend, and its network.
func (x *launcher) runsIn(c *ui.Ctx) {
	heading(c, "head", "Runs in", "")
	pick := backendPick{chosen: &x.backend, setting: settingBackend(x.h), offered: x.h.State().Machine.Sandboxes}
	sandboxToggles(c, &x.sandbox, &x.openNet, pick, "agent/launcher#runs-in")
}

// toggleRow is a toggle with its label, and a note at the right.
func toggleRow(c *ui.Ctx, key any, label, note string, on *bool, disabled bool, help string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if widget.Toggle(c, "t", *on, widget.ToggleOpts{Label: label, Disabled: disabled, Help: help}) {
			*on = !*on
		}
		fg := th.C(theme.Fg)
		if disabled {
			fg = th.C(theme.FgFaint)
		}
		c.Text("l", label, uiFont(12.5, fg))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("n", note, monoFont(10, th.C(theme.FgFaint)))
	})
}

// backendPick is the sandbox backend a session runs in: chosen from those this machine offers, sandbox.backend
// until one is.
type backendPick struct {
	chosen  *string
	offered []string
	setting string
}

func (p backendPick) current() string {
	if p.chosen != nil && *p.chosen != "" {
		return *p.chosen
	}
	return p.setting
}

// settingBackend is sandbox.backend.
func settingBackend(h plugin.Host) string {
	v, _, _ := h.Options().Get("sandbox.backend", registry.At{})
	s, _ := v.(string)
	return s
}

// sandboxToggles are where an agent runs: in a sandbox, in which backend when this machine has more than one, and
// with an open network there.
func sandboxToggles(c *ui.Ctx, on, openNet *bool, pick backendPick, help string) {
	toggleRow(c, "sandbox", "Sandbox", sandboxNoun(pick.current())+" · gateway for git & gh", on, false, help)
	if *on && pick.chosen != nil && len(pick.offered) > 1 {
		c.Row("backend", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			labels := make([]string, len(pick.offered))
			for i, b := range pick.offered {
				labels[i] = strings.ToUpper(b)
			}
			at := slices.Index(pick.offered, pick.current())
			c.Text("l", "Sandbox type", uiFont(12.5, c.Theme().C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Segmented(c, "pick", labels, &at, widget.SegmentedOpts{Label: "Sandbox type", Help: help}) {
				*pick.chosen = pick.offered[at]
			}
		})
	}
	toggleRow(c, "network", "Open network", "ask per new host when off", openNet, !*on, help)
}

// sandboxNoun is what a backend runs an agent in.
func sandboxNoun(backend string) string {
	if backend == "" || backend == "docker" {
		return "container"
	}
	return backend + " sandbox"
}

// side is the recent prompts, and what starting will do.
func (x *launcher) side(c *ui.Ctx) {
	th := c.Theme()
	heading(c, "head", "Recent prompts", "")
	sessions := slices.Clone(x.h.State().Sessions)
	sessions = slices.DeleteFunc(sessions, func(se core.SessionState) bool { return strings.TrimSpace(se.Prompt) == "" })
	slices.SortStableFunc(sessions, func(a, b core.SessionState) int { return b.Created.Compare(a.Created) })
	c.Column("recent", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		if len(sessions) == 0 {
			c.Text("none", "Prompts you start sessions with show here.", uiFont(12, th.C(theme.FgFaint)))
		}
		for _, se := range sessions[:min(len(sessions), 5)] {
			meta := []string{string(se.Kind)}
			for _, p := range se.Projects {
				meta = append(meta, p.Name)
			}
			if !se.Created.IsZero() {
				meta = append(meta, ago(c.Now(), se.Created))
			}
			if widget.Clickable(c, "recent."+string(se.ID), ui.Style{Dir: ui.Column, W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 4, Fill: th.C(tLaunchCard),
				Stroke: th.C(tLaunchCardLine), Border: 1, Radius: 8, Clip: true, Label: firstLine(se.Prompt), Help: "agent/launcher#recent-prompts"},
				func(c *ui.Ctx) {
					c.Text("p", firstLine(se.Prompt), uiFont(12.5, th.C(theme.Fg)))
					c.Text("m", strings.Join(meta, " · "), monoFont(10, th.C(theme.FgFaint)))
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
				c.Text("k", row[0], monoFont(10.5, th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("v", row[1], monoFont(10.5, th.C(theme.Fg)))
			})
		}
	})
}

// reuse fills the launcher from an earlier session: its prompt, kind and repos.
func (x *launcher) reuse(se core.SessionState) {
	x.prompt, x.ticket, x.chosen = se.Prompt, se.Ticket, nil
	if i := slices.IndexFunc(x.kinds, func(k host.KindSpec) bool { return k.Kind == se.Kind }); i >= 0 {
		x.kind = i
	}
	for _, p := range se.Projects {
		x.addRepo(core.RepoID(cmpOr(p.Repo, p.Dir)))
	}
	x.focusField = true
}

// preflight is what starting will do: the worktrees, the repos to fetch, the agent, where it runs.
func (x *launcher) preflight() [][2]string {
	k, ok := x.current()
	if !ok {
		return [][2]string{{"kinds", "none"}}
	}
	worktrees := map[host.Worktrees]string{host.WorktreesNow: "now, from a fetched base", host.WorktreesStartWork: "at Start work",
		host.WorktreesPR: "the PR's head, read-only", host.WorktreesPinned: "pinned, read-only", host.WorktreesInit: "a new repo",
		host.WorktreesNone: "none"}[k.Worktrees]
	if k.Kind == core.KindDirect {
		worktrees = "none: the directory itself"
	}
	rows := [][2]string{{"worktrees", worktrees}}
	if takesRepos(k) && k.Worktrees != host.WorktreesStartWork {
		rows = append(rows, [2]string{"repos to fetch", fmt.Sprint(len(x.chosen))})
	}
	agent := "none"
	switch {
	case x.provider != "":
		agent = strings.TrimSpace(providerName(x.provider) + " " + x.model)
	case !x.noAgent && x.providersState().Loading():
		agent = "waiting on the probe"
	case !x.noAgent:
		agent = "not picked"
	}
	rows = append(rows, [2]string{"agent", agent}, [2]string{"prompt pack", cmpOr(k.Pack, "none")})
	runs, network := "on this machine", "this machine's"
	if x.sandbox {
		runs, network = "in a "+sandboxNoun(backendPick{chosen: &x.backend, setting: settingBackend(x.h)}.current()), "ask per new host"
		if x.openNet {
			network = "open"
		}
	}
	return append(rows, [2]string{"agent runs", runs}, [2]string{"network", network})
}

func (x *launcher) footer(c *ui.Ctx) {
	th := c.Theme()
	k, _ := x.current()
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Column("about", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			if x.err != "" {
				c.Text("err", x.err, monoFont(11, th.C(theme.Error)))
				return
			}
			c.Text("t", k.About, monoFont(11, th.C(theme.FgMuted)))
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
