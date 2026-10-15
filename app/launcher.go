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
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
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

// launcher is the new-session launcher (ui.md 04): prompt, mode, provider, repos, worker and sandbox. Its modes are
// the worker's kinds of session, so a kind a plugin adds there is one more; Start runs session.start.
type launcher struct {
	disposer
	sh *shell
	h  ext.Host

	open       bool
	focusField bool // focus the mode's first field next frame
	gen        int  // which opening the worker's answers are for

	worker    core.WorkerID
	kinds     []worker.KindSpec
	providers []worker.ProviderInfo
	repos     []core.Repo
	loadErr   string
	wantKind  core.Kind // asked for before the kinds arrived

	kind                                          int
	prompt, ticket, pr, dir, name, parent, branch string
	template, repoPath, variant                   string
	provider, model, effort                       string
	chosen                                        []core.RepoID
	clones                                        map[core.RepoID]string // a URL to clone onto the worker, by a chosen repo it lacks
	cloning                                       core.RepoID            // the repo whose Clone popover is open
	cloneURL                                      string
	cloneFocus                                    bool
	sandbox, openNet                              bool
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
	t       *registry.Ticket
	err     string
}

func (x *launcher) Manifest() ext.Manifest { return ext.Manifest{ID: "launcher"} }

func (x *launcher) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	hasWorker := func(registry.Args) bool { return x.sh.sessions != nil && len(x.sh.sessions.clients()) > 0 }
	x.add(h.Command(registry.Command{ID: "session.new",
		Title:     "New session: the launcher, with {kind}, {prompt}, {ticket}, {pr}, {dir}, {repos}, {provider} or {worker} filled in",
		Available: hasWorker, Run: func(_ registry.Ctx, a registry.Args) error { x.openWith(a); return nil }}))
	x.add(h.Command(registry.Command{ID: "session.launch", Title: "Start the session the launcher holds",
		Available: func(registry.Args) bool { return x.open }, Run: func(registry.Ctx, registry.Args) error { return x.start() }}))
	x.add(h.Keymap(registry.Keymap{Mode: LauncherKeyMode, Keys: "<D-CR>", Command: "session.launch", Desc: "Start the session"}))
	x.add(h.View(OverlayPrefix+"launcher", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "launcher", Fn: x.build}
	})))
	return nil
}

// openWith opens the launcher afresh, with what a command gave filled in, and asks the worker for its kinds,
// providers and repos.
func (x *launcher) openWith(a registry.Args) {
	spec := specFrom(a)
	*x = launcher{disposer: x.disposer, sh: x.sh, h: x.h, gen: x.gen + 1, open: true, focusField: true,
		worker: spec.Worker, wantKind: core.Kind(a.String("kind")), prompt: spec.Prompt, ticket: spec.Ticket, pr: spec.PR, dir: spec.Dir,
		name: spec.Name, template: spec.Template, variant: spec.Variant, provider: spec.Provider, model: spec.Model, effort: spec.Effort, chosen: spec.Repos,
		sandbox: spec.Sandbox.Container, openNet: spec.Sandbox.Network == "open", branch: spec.Branch,
		card: ticketCard{key: strings.TrimSpace(spec.Ticket)}}
	if x.template == "" {
		x.template = worker.Templates[0]
	}
	if _, given := a["sandbox"]; !given {
		v, _, _ := x.h.Options().Get("sandbox.default", registry.At{})
		x.sandbox = v == "sandbox"
	}
	x.load(a.String("provider") != "")
}

func (x *launcher) load(providerGiven bool) {
	gen, id := x.gen, x.worker
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var (
			kinds     []worker.KindSpec
			providers []worker.ProviderInfo
			repos     []core.Repo
			err       error
		)
		if x.sh.sessions == nil {
			err = errors.New("no worker is connected")
		} else if c, e := x.sh.sessions.worker(id); e != nil {
			err = e
		} else if kinds, err = c.Kinds(ctx); err == nil {
			if providers, err = c.Providers(ctx); err == nil {
				repos, err = c.Repos(ctx)
			}
		}
		x.h.Do(func() {
			if gen != x.gen {
				return
			}
			x.kinds, x.providers, x.repos = kinds, providers, repos
			if err != nil {
				x.loadErr = err.Error()
			}
			if i := slices.IndexFunc(kinds, func(k worker.KindSpec) bool { return k.Kind == x.wantKind }); i >= 0 {
				x.kind = i
			}
			if !providerGiven {
				x.pickProvider(defaultProvider(providers))
			}
		})
		x.h.Redraw()
	}()
}

// defaultProvider is the first provider that's logged in, else the first.
func defaultProvider(ps []worker.ProviderInfo) string {
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
	for _, p := range x.providers {
		if p.ID == id {
			x.model = defaultModel(p)
		}
	}
}

func (x *launcher) current() (worker.KindSpec, bool) {
	if x.kind < len(x.kinds) {
		return x.kinds[x.kind], true
	}
	return worker.KindSpec{}, false
}

func startsFrom(k worker.KindSpec) worker.From {
	if k.From == "" {
		return worker.FromPrompt
	}
	return k.From
}

// takesRepos says whether the kind starts on repos the launcher picks.
func takesRepos(k worker.KindSpec) bool {
	f := startsFrom(k)
	return f == worker.FromPrompt || f == worker.FromPR
}

// missing is what the session still needs before it can start, or "".
func (x *launcher) missing() string {
	k, ok := x.current()
	switch {
	case !ok:
		return "waiting for the worker"
	case k.NeedsRepo && len(x.chosen) == 0:
		return "pick a repo"
	case takesRepos(k) && x.uncloned() != "":
		return fmt.Sprintf("clone %s onto %s, or remove it", x.uncloned(), x.workerName())
	case startsFrom(k) == worker.FromPrompt && x.card.busy:
		return "wait for " + x.card.asked + " from " + x.card.source
	case startsFrom(k) == worker.FromPR && strings.TrimSpace(x.pr) == "":
		return "paste the pull request's URL"
	case startsFrom(k) == worker.FromName && strings.TrimSpace(x.name) == "":
		return "name the repo"
	case startsFrom(k) == worker.FromDir && strings.TrimSpace(x.dir) == "":
		return "pick a directory"
	}
	return ""
}

// chosenVariant is the kind's variant the launcher holds: the one picked, else its first; "" for a kind with none.
func (x *launcher) chosenVariant(k worker.KindSpec) string {
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
func (x *launcher) variantField(c *ui.Ctx, k worker.KindSpec) {
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

// home expands a leading ~ for this machine's worker.
func (x *launcher) home(p string) string {
	p = strings.TrimSpace(p)
	if (x.worker == "" || x.worker == core.LocalWorker) && strings.HasPrefix(p, "~") {
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
		"effort": x.effort, "worker": string(x.worker), "sandbox": x.sandbox, "branch": strings.TrimSpace(x.branch)}
	if x.sandbox {
		a["network"] = "ask"
		if x.openNet {
			a["network"] = "open"
		}
	}
	if v := x.chosenVariant(k); v != "" {
		a["variant"] = v
	}
	switch startsFrom(k) {
	case worker.FromPrompt:
		a["ticket"] = strings.TrimSpace(x.ticket)
		if t := x.ticketHere(); t != nil {
			a["context"] = t.Context
		}
	case worker.FromPR:
		a["pr"] = strings.TrimSpace(x.pr)
	case worker.FromName:
		a["name"], a["template"], a["dir"] = strings.TrimSpace(x.name), x.template, x.home(x.parent)
	case worker.FromDir:
		a["dir"] = x.home(x.dir)
	}
	if takesRepos(k) {
		var repos []string
		for _, r := range x.chosen {
			repos = append(repos, string(r))
		}
		a["repos"] = repos
		if clone := x.toClone(); len(clone) > 0 {
			a["clone"] = clone
		}
	}
	return a
}

// toClone is session.start's {clone}: the URL each chosen repo the worker lacks is cloned from.
func (x *launcher) toClone() map[string]any {
	out := map[string]any{}
	for _, r := range x.chosen {
		if url := x.clones[r]; url != "" && x.lacks(r) {
			out[string(r)] = url
		}
	}
	return out
}

// local says whether the session runs on this machine's worker.
func (x *launcher) local() bool { return x.worker == "" || x.worker == core.LocalWorker }

// lacks says whether the worker hasn't got a chosen repo, once it has said which it has: one it hasn't registered,
// unless it's a path this machine's worker registers as the session starts.
func (x *launcher) lacks(r core.RepoID) bool {
	if len(x.kinds) == 0 || x.loadErr != "" || x.local() && filepath.IsAbs(string(r)) {
		return false
	}
	return !slices.ContainsFunc(x.repos, func(c core.Repo) bool { return c.ID == r || c.Path == string(r) })
}

// uncloned is the first chosen repo the worker lacks that isn't to be cloned, by name; "" if none.
func (x *launcher) uncloned() string {
	for _, r := range x.chosen {
		if x.lacks(r) && x.clones[r] == "" {
			return x.repoName(r)
		}
	}
	return ""
}

// openClone opens r's Clone popover, its URL the local checkout's remote when r is one.
func (x *launcher) openClone(r core.RepoID) {
	x.cloning, x.cloneURL, x.cloneFocus = r, x.clones[r], true
	if x.cloneURL == "" && filepath.IsAbs(string(r)) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		x.cloneURL = gitx.Repo{Dir: string(r)}.RemoteURL(ctx)
	}
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
	gen := x.gen
	go func() {
		err := x.h.Run(context.Background(), command, a)
		x.h.Do(func() {
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
		x.h.Redraw()
	}()
	return nil
}

// continues is the session the ticket field's ticket already has, when the kind starts from a prompt: Start
// continues it rather than starting another (ticketSession).
func (x *launcher) continues() (core.Session, bool) {
	if k, ok := x.current(); !ok || startsFrom(k) != worker.FromPrompt {
		return nil, false
	}
	return ticketSession(x.h, x.ticket)
}

// saveAsJob hands what the launcher holds to the New job dialog: the prompt, the agent, where it runs.
func (x *launcher) saveAsJob() {
	a := registry.Args{"prompt": strings.TrimSpace(x.prompt), "provider": x.provider, "model": x.model, "effort": x.effort,
		"worker": string(x.worker), "sandbox": x.sandbox}
	x.close()
	if err := x.h.Run(context.Background(), "jobs.new", a); err != nil {
		x.h.Log().Error("launcher", "err", err)
	}
}

func (x *launcher) close() { x.open, x.gen = false, x.gen+1 }

func (x *launcher) build(c *ui.Ctx) {
	if !x.open {
		return
	}
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
		if w := x.workerName(); w != "" {
			meta = append(meta, "worker "+w)
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

// workerName is the worker the session will run on, as the user named it.
func (x *launcher) workerName() string {
	for _, w := range x.h.State().Workers {
		if w.ID == x.worker || x.worker == "" && w.ID == core.LocalWorker {
			return w.Title()
		}
	}
	return string(x.worker)
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
	switch {
	case r.Cancelled:
		x.close()
	case r.Submitted && key == "repo-path":
		x.addRepo(core.RepoID(x.home(x.repoPath)))
		x.repoPath = ""
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
		case worker.FromPR:
			x.field(c, "pr", &x.pr, "Pull request", prPlaceholder, true, ui.Flex(1), 1)
		case worker.FromName:
			c.Row("repo", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "name", &x.name, "Name", namePlaceholder, true, ui.Flex(1), 1)
				if t, ok := widget.Select(c, "template", "template: "+x.template, worker.Templates,
					widget.SelectOpts{Label: "Template", Help: "agent/launcher#prompt"}); ok {
					x.template = t
				}
				x.field(c, "parent", &x.parent, "Where it goes", parentPlaceholder, false, ui.Flex(1), 1)
			})
		case worker.FromDir:
			x.field(c, "dir", &x.dir, "Directory", dirPlaceholder, true, ui.Flex(1), 1)
		}
		x.field(c, "text", &x.prompt, "Prompt", promptPlaceholder, f == worker.FromPrompt || f == worker.FromNothing, ui.Flex(1), 8)
		if f == worker.FromPrompt {
			x.lookTicket(c)
			x.ticketCard(c)
			c.Row("extra", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				x.field(c, "ticket", &x.ticket, "Ticket", "ticket: INF-2231", false, ui.Px(220), 1)
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
			msg := "Asking the worker…"
			if x.loadErr != "" {
				msg = x.loadErr
			}
			c.Text("wait", msg, uiFont(12, th.C(theme.FgFaint)))
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
		c.Row("cards", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			for _, p := range x.providers {
				x.providerCard(c, p)
			}
			x.noAgentCard(c, k)
		})
	})
	c.Row("where", ui.Style{W: ui.Flex(1), Gap: 24}, func(c *ui.Ctx) {
		if takesRepos(k) {
			c.Column("repos", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) { x.reposSection(c, k) })
		}
		c.Column("run", ui.Style{W: ui.Flex(1), Gap: 10}, x.runOn)
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
	x.card.busy, x.card.source = true, src.Label()
	gen := x.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		t, err := registry.LookupTicket(ctx, src, key)
		x.h.Do(func() {
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
		x.h.Redraw()
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
		c.Text("ticket-wait", "Looking up "+x.card.asked+" in "+x.card.source+"…", monoFont(10.5, th.C(theme.FgFaint)))
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
func (x *launcher) providerCard(c *ui.Ctx, p worker.ProviderInfo) {
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
			if p.ID == defaultProvider(x.providers) {
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
func modelSelects(c *ui.Ctx, p worker.ProviderInfo, model, effort, help string) (string, string, bool) {
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
func defaultModel(p worker.ProviderInfo) string {
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
func providerNote(p worker.ProviderInfo, usage []core.ProviderUsage) string {
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
func (x *launcher) noAgentCard(c *ui.Ctx, k worker.KindSpec) {
	th := c.Theme()
	on := x.provider == ""
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
		x.pickProvider("")
	}
}

func (x *launcher) reposSection(c *ui.Ctx, k worker.KindSpec) {
	th := c.Theme()
	note := "one worktree each · same branch"
	if startsFrom(k) == worker.FromPR {
		note = "the PR's repo, found by its remote if none"
	}
	heading(c, "head", "Repositories", note)
	c.Row("chips", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		for _, r := range slices.Clone(x.chosen) {
			name := x.repoName(r)
			tone := th.C(tLaunchRepo)
			if x.lacks(r) {
				tone = th.C(theme.Warn)
			}
			c.Row("repo."+string(r), ui.Style{Pad: ui.Insets{L: 10, R: 4, T: 3, B: 3}, Gap: 6, Cross: ui.Center, Radius: 12, Stroke: tone,
				Border: 1, Role: ui.RoleGroup, Label: name}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: tone}, nil)
				c.Text("name", name, monoFont(11.5, th.C(theme.Fg)))
				if x.lacks(r) {
					x.cloneChip(c, r, name)
				}
				if widget.IconButton(c, "remove", "✕", widget.IconButtonOpts{Label: "Remove " + name, Help: "agent/launcher#repositories", Size: 18}) {
					x.chosen = slices.DeleteFunc(x.chosen, func(id core.RepoID) bool { return id == r })
				}
			})
		}
		var options []string
		for _, r := range x.repos {
			if !slices.Contains(x.chosen, r.ID) {
				options = append(options, string(r.ID))
			}
		}
		if len(options) > 0 {
			if v, ok := widget.Select(c, "add", "+ add", options, widget.SelectOpts{Label: "Add a repo", Help: "agent/launcher#repositories"}); ok {
				x.addRepo(core.RepoID(v))
			}
		}
	})
	x.field(c, "repo-path", &x.repoPath, "Add a repo by its path", "or a checkout's path, ⏎ adds it", false, ui.Flex(1), 1)
	if k.Worktrees == worker.WorktreesNow || k.Worktrees == worker.WorktreesStartWork {
		c.Row("branch", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("l", "Branch", uiFont(12.5, th.C(theme.FgMuted)))
			x.field(c, "branch", &x.branch, "Branch", "named from the ticket and prompt", false, ui.Flex(1), 1)
		})
	}
}

// cloneChip is a chip's "not on <worker>: Clone", whose popover takes the URL to clone it from as the session starts.
func (x *launcher) cloneChip(c *ui.Ctx, r core.RepoID, name string) {
	const help = "agent/launcher#repositories"
	th := c.Theme()
	note := "not on " + x.workerName() + ":"
	if x.clones[r] != "" {
		note = "clones onto " + x.workerName() + " at start:"
	}
	c.Text("missing", note, monoFont(10.5, th.C(theme.Warn)))
	anchor := c.Next("clone")
	label := "Clone"
	if x.clones[r] != "" {
		label = "Change"
	}
	if widget.Button(c, "clone", label, widget.ButtonOpts{Ghost: true, Help: help}) {
		x.openClone(r)
	}
	open := x.cloning == r
	widget.Popover(c, "clonepop", anchor, &open, widget.PopoverOpts{Label: "Clone " + name, Width: 460, Help: help}, func(c *ui.Ctx) {
		c.Text("t", "Clone "+name+" onto "+x.workerName()+" from", uiFont(12.5, th.C(theme.Fg)))
		if x.cloneFocus {
			c.Focus(c.Next("url"))
			x.cloneFocus = false
		}
		res := widget.Input(c, "url", &x.cloneURL, widget.InputOpts{Label: "Clone from", Placeholder: "a git URL: git@github.com:acme/pricing.git",
			Help: help, KeyMode: LauncherKeyMode})
		c.Text("n", "with the worker's own git and credentials, before the session starts", monoFont(10, th.C(theme.FgFaint)))
		ok := strings.TrimSpace(x.cloneURL) != ""
		if (widget.Button(c, "ok", "Clone at start", widget.ButtonOpts{Primary: true, Disabled: !ok, Help: help}) || res.Submitted) && ok {
			if x.clones == nil {
				x.clones = map[core.RepoID]string{}
			}
			x.clones[r], open = strings.TrimSpace(x.cloneURL), false
		}
		if res.Cancelled {
			open = false
		}
	})
	if !open && x.cloning == r {
		x.cloning = ""
	}
}

// repoName is a repo as the launcher shows it: its ID, or a path's last part.
func (x *launcher) repoName(r core.RepoID) string {
	for _, x := range x.repos {
		if x.ID == r || x.Path == string(r) {
			return string(x.ID)
		}
	}
	return filepath.Base(string(r))
}

// runOn is where the session runs: the worker, the sandbox and its network.
func (x *launcher) runOn(c *ui.Ctx) {
	heading(c, "head", "Run on", "")
	var labels []string
	var ids []core.WorkerID
	current := ""
	for _, w := range x.h.State().Workers {
		if !w.Connected {
			continue
		}
		l := w.Title()
		if w.Cores > 0 {
			l += fmt.Sprintf(" · %d cores · cpu %d%%", w.Cores, int(w.CPU*100+.5))
		}
		labels, ids = append(labels, l), append(ids, w.ID)
		if w.ID == x.worker || x.worker == "" && w.ID == core.LocalWorker {
			current = l
		}
	}
	if current == "" && len(labels) > 0 {
		current = labels[0]
	}
	if v, ok := widget.Select(c, "worker", current, labels, widget.SelectOpts{Label: "Worker", Help: "agent/launcher#run-on", Width: 320}); ok {
		if i := slices.Index(labels, v); i >= 0 && ids[i] != x.worker {
			x.worker, x.kinds, x.providers, x.repos, x.chosen = ids[i], nil, nil, nil, nil
			x.gen++
			x.load(false)
		}
	}
	sandboxToggles(c, &x.sandbox, &x.openNet, "agent/launcher#run-on")
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

// sandboxToggles are where an agent runs: in a sandbox, and with an open network there.
func sandboxToggles(c *ui.Ctx, sandbox, openNet *bool, help string) {
	toggleRow(c, "sandbox", "Sandbox", "container · gateway for git & gh", sandbox, false, help)
	toggleRow(c, "network", "Open network", "ask per new host when off", openNet, !*sandbox, help)
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
	if i := slices.IndexFunc(x.kinds, func(k worker.KindSpec) bool { return k.Kind == se.Kind }); i >= 0 {
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
		return [][2]string{{"worker", "asking…"}}
	}
	worktrees := map[worker.Worktrees]string{worker.WorktreesNow: "now, from a fetched base", worker.WorktreesStartWork: "at Start work",
		worker.WorktreesPR: "the PR's head, read-only", worker.WorktreesPinned: "pinned, read-only", worker.WorktreesInit: "a new repo",
		worker.WorktreesNone: "none"}[k.Worktrees]
	if k.Kind == core.KindDirect {
		worktrees = "none: the directory itself"
	}
	rows := [][2]string{{"worktrees", worktrees}}
	if takesRepos(k) && k.Worktrees != worker.WorktreesStartWork {
		rows = append(rows, [2]string{"repos to fetch", fmt.Sprint(len(x.chosen))})
	}
	if n := len(x.toClone()); takesRepos(k) && n > 0 {
		rows = append(rows, [2]string{"repos to clone", fmt.Sprint(n)})
	}
	agent := "none"
	if x.provider != "" {
		agent = strings.TrimSpace(providerName(x.provider) + " " + x.model)
	}
	rows = append(rows, [2]string{"agent", agent}, [2]string{"prompt pack", cmpOr(k.Pack, "none")})
	runs, network := "on the worker", "the worker's"
	if x.sandbox {
		runs, network = "in a container", "ask per new host"
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
		if widget.Button(c, "start", label, widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: x.busy || k.Kind == "",
			Help: "agent/launcher"}) {
			_ = x.start()
		}
	})
}
