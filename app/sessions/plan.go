package sessions

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Plan mode's views: a plan doc rendered, with its threads in the margin, and the docs beside it with the
// plan's source, projects and readiness.
const (
	PlanView     = "plan"
	PlanDocsView = kit.PlanDocsView
)

var tPlanMark = theme.Declare("plan.mark", "amber")

// Plan is a plan session's docs, read as documents, with threads anchored to their lines, readiness, and
// Start work. It drives the requests and sessions through their commands.
type Plan struct {
	kit.Disposer
	h    plugin.Host
	life context.Context // ends when it's deactivated
	by   map[core.SessionID]*planState

	dialog   *startWork // the Start work dialog, while it's open
	startGen int        // which opening of it the host's answers are for
}

// planState is one session's plan views: the doc in front, and each doc's presentation.
type planState struct {
	doc     string // the doc in front, by its name at the workspace root
	docSel  int
	focus   bool // give the doc focus next frame
	present map[string]kit.PresentState

	docs []planDoc

	edits  map[string]int    // the agent's edits to each doc, by path
	editIn kit.TranscriptKey // in this transcript
}

// planDoc is a markdown file at the workspace root, as it was when last changed.
type planDoc struct {
	name, path string
	data       []byte       // as read
	buf        *text.Buffer // its text, as the presenter reads it
	title      string       // its first level-1 heading
}

func (x *Plan) Manifest() plugin.Manifest { return plugin.Manifest{ID: "plan"} }

func (x *Plan) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.life = h, x.Lifetime()
	x.by = map[core.SessionID]*planState{}
	x.Add(h.Contribute(registry.KindEntry{Source: "builtin:plan", SessionKind: registry.Kind{KindSpec: host.PlanKind,
		LabelFunc: func(se core.SessionState) string {
			if se.Started {
				return "plan → work"
			}
			return "plan"
		}}}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Keymap(registry.Keymap{Mode: StartWorkKeyMode, Keys: "<D-CR>", Command: "plan.start-work.run", Desc: "Start work"}))
	for id, v := range x.views() {
		x.Add(h.View(id, v))
	}
	x.Add(h.Contribute(uireg.RailEntry{Name: "plan-docs", Icon: "doc", Title: "Plan docs", Command: "view.toggle", Args: planDocsArgs(),
		Order: 45, Shown: func(string) bool { se, ok := x.front(); return ok && se.Planning() }}))
	x.Add(kit.OnDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if _, ok := ev.(core.SessionFocused); ok {
			h.Do(x.openIfPlanning)
		}
	})))
	h.Do(x.openIfPlanning)
	return nil
}

func (x *Plan) commands() []registry.Command {
	hasPlan := func(registry.Args) bool {
		se, ok := x.front()
		return ok && se.StartWork
	}
	return []registry.Command{
		{ID: "plan.open", Title: "Open the plan of the session in front: its docs ({doc}, PLAN.md first), threads and readiness", Available: hasPlan,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.open(ctx, a.String("doc")) }},
		{ID: "plan.start-work", Title: "Start work on the plan in front: a dialog of how, which runs session.start-work",
			Available: func(registry.Args) bool { se, ok := x.front(); return ok && se.Planning() },
			Run:       func(registry.Ctx, registry.Args) error { return x.openStartWork() }},
		{ID: "plan.start-work.run", Title: "Start work as the Start work dialog says", Available: func(registry.Args) bool { return x.dialog != nil },
			Run: func(registry.Ctx, registry.Args) error { return x.runStartWork() }},
	}
}

func (x *Plan) views() map[string]uireg.View {
	return map[string]uireg.View{
		kit.OverlayPrefix + "plan.start-work": uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
			return &ui.Func{Key: "startwork", Fn: x.startWorkView}
		}),
		PlanView:     planView{x},
		PlanDocsView: planDocsView{x},
	}
}

func (x *Plan) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

func (x *Plan) state(id core.SessionID) *planState {
	st := x.by[id]
	if st == nil {
		st = &planState{present: map[string]kit.PresentState{}}
		x.by[id] = st
	}
	return st
}

// openIfPlanning shows the plan when the window is on a session that's planning.
func (x *Plan) openIfPlanning() {
	se, ok := x.front()
	if !ok || !se.Planning() || filepath.Clean(x.h.Here().Project) != filepath.Clean(kit.SessionDir(se)) {
		return
	}
	if err := x.h.Run(context.Background(), "plan.open", nil); err != nil {
		x.h.Log().Warn("plan", "err", err)
	}
}

// open shows the plan's views: the doc in the main area, the docs docked at the left.
func (x *Plan) open(ctx context.Context, doc string) error {
	se, ok := x.front()
	if !ok {
		return fmt.Errorf("no session is in front")
	}
	st := x.state(se.ID)
	if doc != "" {
		st.doc = doc
	}
	shown := map[string]bool{}
	for _, v := range x.h.Layout().Visible() {
		shown[v.ID] = true
	}
	if !shown[PlanDocsView] {
		if err := x.h.Run(ctx, "view.open", planDocsArgs()); err != nil {
			return err
		}
	}
	st.focus = true
	if shown[PlanView] {
		return nil
	}
	return x.h.Run(ctx, "view.open", registry.Args{"id": PlanView, "place": "tab"})
}

// docs are the markdown files at the session's workspace root, PLAN.md first, read through the window's reads; loading
// while the folder's first read is under way.
func (x *Plan) docs(se core.SessionState, st *planState) (docs []planDoc, loading bool) {
	rd, ok := kit.ReadsOf(x.h)
	if se.Dir == "" || !ok {
		return nil, false
	}
	entries, dst := rd.Dir(se.Dir)
	was := map[string]planDoc{}
	for _, d := range st.docs {
		was[d.path] = d
	}
	st.docs = []planDoc{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") || slices.Contains(host.NotesFiles, e.Name()) {
			continue
		}
		path := filepath.Join(se.Dir, e.Name())
		f, _ := rd.File(path)
		d, ok := was[path]
		switch {
		case f.Data == nil && ok, ok && kit.SameBytes(d.data, f.Data):
			st.docs = append(st.docs, d)
			continue
		case f.Data == nil:
			continue
		}
		title, _ := kit.MarkdownTitle(x.h, string(f.Data))
		st.docs = append(st.docs, planDoc{name: e.Name(), path: path, data: f.Data, buf: text.New(bytes.Clone(f.Data), text.Author{}), title: title})
	}
	rank := func(d planDoc) int {
		if strings.EqualFold(d.name, "PLAN.md") {
			return 0
		}
		return 1
	}
	slices.SortFunc(st.docs, func(a, b planDoc) int {
		return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(strings.ToLower(a.name), strings.ToLower(b.name)))
	})
	return st.docs, dst.Loading()
}

// current is the doc in front: the one chosen, else the first.
func (x *Plan) current(st *planState, docs []planDoc) (planDoc, bool) {
	for i, d := range docs {
		if d.name == st.doc {
			st.docSel = i
			return d, true
		}
	}
	if len(docs) == 0 {
		return planDoc{}, false
	}
	st.doc, st.docSel = docs[0].name, 0
	return docs[0], true
}

// onDoc reports whether an anchor is on the doc.
func onDoc(a *core.Anchor, d planDoc) bool {
	return a != nil && a.Project == core.DocsProject && filepath.ToSlash(a.File) == d.name
}

// docThreads are the threads on a doc.
func docThreads(se core.SessionState, d planDoc) []core.Thread {
	var out []core.Thread
	for _, t := range se.Threads {
		if onDoc(t.Anchor, d) && !t.Btw {
			out = append(out, t)
		}
	}
	return out
}

// revs is how many times the agent has edited a doc, counted again when the transcript changes.
func (st *planState) revs(se core.SessionState, path string) int {
	if of := kit.TranscriptOf(se); st.edits == nil || st.editIn != of {
		st.edits, st.editIn = map[string]int{}, of
	}
	n, ok := st.edits[path]
	if !ok {
		for _, it := range se.Transcript {
			if it.Diff != nil && kit.SameFile(se, it.Diff.Path, path) {
				n++
			}
		}
		st.edits[path] = n
	}
	return max(n, 1)
}

// editing is the doc the agent's turn is editing now, if it's one of the plan's.
func editing(se core.SessionState) string {
	if !se.Agent.Working {
		return ""
	}
	for i := len(se.Transcript) - 1; i >= 0; i-- {
		it := se.Transcript[i]
		if it.Kind == core.ItemUser {
			return ""
		}
		if it.Diff == nil {
			continue
		}
		path := it.Diff.Path
		if filepath.IsAbs(path) && filepath.Dir(filepath.Clean(path)) == filepath.Clean(se.Dir) && strings.EqualFold(filepath.Ext(path), ".md") {
			return filepath.Base(path)
		}
		return ""
	}
	return ""
}

func (x *Plan) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "plan", Severity: core.SevError, Source: "plan", Text: err.Error()})
	}
}

type planView struct{ x *Plan }

func (v planView) Title(string) string {
	if se, ok := v.x.front(); ok {
		if st := v.x.state(se.ID); st.doc != "" {
			return st.doc
		}
	}
	return "Plan"
}
func (v planView) EdgeToEdge() {}
func (v planView) ModeLabel(string) (string, string) {
	return "PLAN", tPlanMark.Name()
}
func (v planView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "plan", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

type planDocsView struct{ x *Plan }

func (v planDocsView) Title(string) string { return "Plan docs" }
func (v planDocsView) EdgeToEdge()         {}
func (v planDocsView) Build(uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "plandocs", Fn: v.x.docsView}
}

// readiness is what holds up Start work, and how far the plan is from it.
func readiness(se core.SessionState) (open int, frac float64) {
	open = len(se.Holding())
	done := 0
	plan := se.PlanThread()
	for _, t := range se.Threads {
		if !t.Open() && !t.Btw && t.ID != plan {
			done++
		}
	}
	if open+done == 0 {
		return 0, 1
	}
	return open, float64(done) / float64(open+done)
}

// docsView is the left side: the plan's docs with their threads, its source, its projects, and readiness.
func (x *Plan) docsView(c *ui.Ctx) {
	th := c.Theme()
	se, ok := x.front()
	c.Column("plandocs", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgSurface), Role: ui.RoleRegion, Label: "Plan docs",
		Help: "agent/plan#docs"}, func(c *ui.Ctx) {
		if !ok {
			kit.NoSessionNote(c, "its plan shows here.")
			return
		}
		st := x.state(se.ID)
		docs, loading := x.docs(se, st)
		x.current(st, docs)
		c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, T: 12, B: 10}, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "PLAN DOCS", kit.KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d %s", len(docs), kit.Plural(len(docs), "file", "files")), kit.MonoFont(10, th.C(theme.FgFaint)))
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if len(docs) == 0 && !loading {
			c.Box("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 12)}, func(c *ui.Ctx) {
				widget.Wrap(c, "t", "No docs yet: the agent writes PLAN.md here.", 220, kit.UIFont(12, th.C(theme.FgFaint)))
			})
		} else {
			res := widget.List(c, "docs", len(docs), &st.docSel, widget.ListOpts{RowHeight: 36, Height: ui.Px(float32(36 * len(docs))), Label: "Plan docs",
				Help: "agent/plan#docs"}, func(c *ui.Ctx, i int, sel bool) {
				x.docRow(c, se, docs[i], sel)
			})
			if res.Changed || res.Activated >= 0 {
				st.doc = docs[st.docSel].name
				st.focus = st.focus || res.Activated >= 0
			}
		}
		c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("source", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 9, Role: ui.RoleGroup, Label: "Source"}, func(c *ui.Ctx) {
			c.Text("k", "SOURCE", kit.KickerFont(th.C(theme.FgFaint)))
			if se.Ticket != "" {
				widget.Chip(c, "ticket", se.Ticket, th.C(theme.Accent), "agent/plan#docs")
			}
			prompt := se.Prompt
			if t, ok := se.Thread(se.PlanThread()); ok && prompt == "" {
				if len(t.Comments) > 0 {
					prompt = t.Comments[0].Body
				}
			}
			if prompt = strings.TrimSpace(core.PlainMarkdown(prompt)); prompt != "" {
				widget.Wrap(c, "p", strings.Join(strings.Fields(prompt), " "), 220, kit.UIFont(12, th.C(theme.FgFaint)))
			}
		})
		if len(se.Projects) > 0 {
			c.Column("projects", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 4), Gap: 8, Role: ui.RoleGroup, Label: "Projects"}, func(c *ui.Ctx) {
				c.Text("k", "PROJECTS", kit.KickerFont(th.C(theme.FgFaint)))
				for i, p := range se.Projects {
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						tone := th.C(theme.StatusQuiet)
						if p.ReadOnly {
							tone = th.C(theme.FgFaint)
						}
						c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: tone}, nil)
						c.Text("n", p.Name, kit.MonoFont(11.5, th.C(theme.Fg)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						c.Text("b", p.Branch, kit.MonoFont(10.5, th.C(theme.FgFaint)))
					})
				}
			})
		}
		c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
		c.Box("rule3", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		open, frac := readiness(se)
		c.Column("ready", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 9, Role: ui.RoleGroup, Label: "Readiness", Help: "agent/plan#readiness"},
			func(c *ui.Ctx) {
				c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("k", "READINESS", kit.KickerFont(th.C(theme.FgFaint)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("n", fmt.Sprintf("%d open", open), kit.MonoFont(10.5, th.C(theme.FgMuted)))
				})
				tone := th.C(tPlanMark)
				if open == 0 {
					tone = th.C(theme.StatusQuiet)
				}
				widget.Meter(c, "bar", frac, widget.MeterOpts{Tone: tone, Label: "Readiness"})
				note := "Start work unlocks when every thread is resolved or deferred."
				switch {
				case se.Started:
					note = "Work started: the agent is implementing the plan."
				case open == 0:
					note = "Ready: every thread is resolved or deferred."
				}
				widget.Wrap(c, "note", note, 220, kit.UIFont(11.5, th.C(theme.FgFaint)))
			})
	})
}

// docRow is a doc in the list: its name, and its threads that need you, or are being worked on, or are open.
func (x *Plan) docRow(c *ui.Ctx, se core.SessionState, d planDoc, sel bool) {
	th := c.Theme()
	needs, working, open := 0, 0, 0
	for _, t := range docThreads(se, d) {
		switch kit.GroupOf(t) {
		case kit.GroupNeedsYou:
			needs++
		case kit.GroupWorking:
			working++
		case kit.GroupOpen:
			open++
		}
	}
	name := th.C(theme.FgMuted)
	if sel {
		name = th.C(theme.Fg)
	}
	c.Row("row", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("sq", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: th.C(tPlanMark)}, nil)
		c.Text("n", d.name, kit.UIFont(13, name))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		switch {
		case needs > 0:
			widget.Chip(c, "n", fmt.Sprint(needs), th.C(theme.StatusNeedsYou), "agent/plan#docs")
		case working > 0:
			widget.Chip(c, "n", fmt.Sprint(working), th.C(theme.StatusWorking), "agent/plan#docs")
		case open > 0:
			widget.Chip(c, "n", fmt.Sprint(open), th.C(theme.FgFaint), "agent/plan#docs")
		default:
			c.Text("n", "–", kit.MonoFont(11, th.C(theme.FgGhost)))
		}
	})
}

// view is the main area: the doc in front rendered, its threads in the margin.
func (x *Plan) view(c *ui.Ctx, vc uireg.ViewCtx) {
	th := c.Theme()
	se, ok := x.front()
	root := c.Next("plan")
	c.Column("plan", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase), Role: ui.RoleRegion, Label: "Plan", Help: "agent/plan"}, func(c *ui.Ctx) {
		if !ok {
			kit.NoSessionNote(c, "its plan shows here.")
			return
		}
		st := x.state(se.ID)
		docs, loading := x.docs(se, st)
		d, has := x.current(st, docs)
		x.header(c, se, st, d, has)
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if !has && loading {
			return
		}
		if !has {
			c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 8}, func(c *ui.Ctx) {
				c.Text("k", "NO PLAN DOCS YET", kit.KickerFont(th.C(tPlanMark)))
				c.Text("t", "The agent drafts PLAN.md at the workspace root; it shows here as it writes.", kit.UIFont(13, th.C(theme.FgFaint)))
			})
			return
		}
		x.document(c, vc, se, st, d, root)
	})
}

func (x *Plan) header(c *ui.Ctx, se core.SessionState, st *planState, d planDoc, has bool) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(48), Pad: ui.PadXY(20, 0), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		widget.Chip(c, "kind", "plan", th.C(tPlanMark), "agent/plan#header")
		title := cmp.Or(d.title, se.Name)
		c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		var who []string
		if se.Agent.Provider != "" {
			who = append(who, strings.ToLower(kit.ProviderName(se.Agent.Provider)))
		}
		if se.Agent.Model != "" {
			who = append(who, se.Agent.Model)
		}
		phase := "work started"
		if se.Planning() {
			phase = "drafting"
		}
		if has {
			phase += fmt.Sprintf(" rev %d", st.revs(se, d.path))
		}
		meta := phase
		if len(who) > 0 {
			meta = strings.Join(who, " ") + " · " + phase
		}
		c.Text("meta", meta, kit.MonoFont(10.5, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if doc := editing(se); doc != "" {
			c.Row("editing", ui.Style{Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.StatusWorking)}, nil)
				c.Text("t", "agent editing "+doc, kit.MonoFont(10.5, th.C(theme.FgMuted)))
			})
		}
		diff := registry.Args{"path": se.Dir}
		if widget.Button(c, "diff", "Preview diff", widget.ButtonOpts{Disabled: !x.h.Available("diff.open", diff), Help: "agent/plan#header"}) {
			x.run("diff.open", diff)
		}
		if se.Started {
			widget.Chip(c, "started", "work started", th.C(theme.StatusQuiet), "agent/plan#start-work")
			return
		}
		open, _ := readiness(se)
		if widget.Button(c, "start", "Start work", widget.ButtonOpts{Primary: true, Disabled: open > 0 || !x.h.Available("session.start-work", nil),
			Help: "agent/plan#start-work"}) {
			x.run("plan.start-work", nil)
		}
	})
}

// document is the doc as a page, drawn by the markdown presenter, with its threads in the margin.
func (x *Plan) document(c *ui.Ctx, vc uireg.ViewCtx, se core.SessionState, st *planState, d planDoc, root ui.ID) {
	pr, ok := uireg.PresenterNamed(x.h, "markdown")
	present, has := kit.PresenterOf(x.h)
	if !ok || !has {
		c.Text("none", "No markdown presenter is registered.", kit.UIFont(13, c.Theme().C(theme.FgFaint)))
		return
	}
	ps := st.present[d.name]
	if ps == nil {
		ps = present.NewPresent()
		st.present[d.name] = ps
	}
	if st.focus {
		ps.Focus()
		st.focus = false
	}
	kicker := fmt.Sprintf("%s · rev %d", d.name, st.revs(se, d.path))
	present.Present(c, x.h, ps, kit.Presentation{Presenter: pr, Path: d.path, Name: d.name, Buf: d.buf, Window: PlanView, Kicker: kicker,
		Root: root, Source: func(line int) {
			x.run("editor.open", registry.Args{"path": d.path, "line": line + 1})
		}}, vc.Width)
}

// planDocsArgs are view.open's args for the Plan docs panel: in the sidebar, alone and fixed.
func planDocsArgs() registry.Args {
	return kit.SidebarArgs("view.open", registry.Args{"id": PlanDocsView, "place": "dock", "edge": "left", "size": 260, "title": "Plan docs"})
}
