package app

import (
	"cmp"
	"context"
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
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Plan mode's views (ui.md 05): a plan doc rendered, with its threads in the margin, and the docs beside it with the
// plan's source, projects and readiness.
const (
	PlanView     = "plan"
	PlanDocsView = "plan.docs"
)

var tPlanMark = theme.Declare("plan.mark", "amber")

// planPlugin is AI-8: a plan session's docs, read as documents, with threads anchored to their lines, readiness, and
// Start work. It drives the requests and sessions through their commands.
type planPlugin struct {
	disposer
	sh *shell
	h  plugin.Host
	by map[core.SessionID]*planState

	dialog   *startWork // the Start work dialog, while it's open
	startGen int        // which opening of it the host's answers are for
}

// planState is one session's plan views: the doc in front, and each doc's presentation.
type planState struct {
	doc     string // the doc in front, by its name at the workspace root
	docSel  int
	focus   bool // give the doc focus next frame
	present map[string]*presentState
	bufs    map[string]*text.Buffer // each doc's text, as the presenter reads it

	docs []planDoc
	read time.Time // when the docs were last read
	seen int       // the transcript's length then
}

// planDoc is a markdown file at the workspace root.
type planDoc struct {
	name, path string
	src        string
	lines      []string
	blocks     []mdBlock
}

func (x *planPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "plan"} }

func (x *planPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.by = map[core.SessionID]*planState{}
	hasPlan := func(registry.Args) bool {
		se, ok := x.front()
		return ok && se.StartWork
	}
	for _, c := range []registry.Command{
		{ID: "plan.open", Title: "Open the plan of the session in front: its docs ({doc}, PLAN.md first), threads and readiness", Available: hasPlan,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.open(ctx, a.String("doc")) }},
		{ID: "plan.start-work", Title: "Start work on the plan in front: a dialog of how, which runs session.start-work",
			Available: func(registry.Args) bool { se, ok := x.front(); return ok && se.Planning() },
			Run:       func(registry.Ctx, registry.Args) error { return x.openStartWork() }},
		{ID: "plan.start-work.run", Title: "Start work as the Start work dialog says", Available: func(registry.Args) bool { return x.dialog != nil },
			Run: func(registry.Ctx, registry.Args) error { return x.runStartWork() }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: StartWorkKeyMode, Keys: "<D-CR>", Command: "plan.start-work.run", Desc: "Start work"}))
	x.add(h.View(OverlayPrefix+"plan.start-work", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "startwork", Fn: x.startWorkView}
	})))
	x.add(h.Contribute(registry.RailEntry{Name: "plan-docs", Icon: "doc", Title: "Plan docs", Command: "view.toggle", Args: planDocsArgs(),
		Order: 45, Shown: func(string) bool { se, ok := x.front(); return ok && se.Planning() }}))
	x.add(h.View(PlanView, planView{x}))
	x.add(h.View(PlanDocsView, planDocsView{x}))
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if _, ok := ev.(core.SessionFocused); ok {
			h.Do(x.openIfPlanning)
		}
	})))
	h.Do(x.openIfPlanning)
	return nil
}

func (x *planPlugin) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

func (x *planPlugin) state(id core.SessionID) *planState {
	st := x.by[id]
	if st == nil {
		st = &planState{present: map[string]*presentState{}, bufs: map[string]*text.Buffer{}}
		x.by[id] = st
	}
	return st
}

// openIfPlanning shows the plan when the window is on a session that's planning.
func (x *planPlugin) openIfPlanning() {
	se, ok := x.front()
	if !ok || !se.Planning() || filepath.Clean(x.sh.cfg.WorkDir) != filepath.Clean(sessionDir(se)) {
		return
	}
	if err := x.h.Run(context.Background(), "plan.open", nil); err != nil {
		x.h.Log().Warn("plan", "err", err)
	}
}

// open shows the plan's views: the doc in the main area, the docs docked at the left.
func (x *planPlugin) open(ctx context.Context, doc string) error {
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

// docs are the markdown files at the session's workspace root, PLAN.md first, read again each second or when the
// agent did something.
func (x *planPlugin) docs(se core.SessionState, st *planState, now time.Time) []planDoc {
	if st.docs != nil && now.Before(st.read.Add(time.Second)) && st.seen == len(se.Transcript) {
		return st.docs
	}
	st.read, st.seen = now, len(se.Transcript)
	st.docs = []planDoc{}
	if se.Dir == "" {
		return st.docs
	}
	entries, _ := os.ReadDir(se.Dir)
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") || slices.Contains(host.NotesFiles, e.Name()) {
			continue
		}
		path := filepath.Join(se.Dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		src := string(b)
		st.docs = append(st.docs, planDoc{name: e.Name(), path: path, src: src, lines: strings.Split(strings.TrimSuffix(src, "\n"), "\n"),
			blocks: mdParse(src)})
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
	return st.docs
}

// current is the doc in front: the one chosen, else the first.
func (x *planPlugin) current(st *planState, docs []planDoc) (planDoc, bool) {
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

// sameFile reports whether a path the agent edited (absolute, or relative to the primary project) is path.
func sameFile(se core.SessionState, edited, path string) bool {
	if !filepath.IsAbs(edited) {
		if p, ok := se.Primary(); ok {
			edited = filepath.Join(p.Dir, edited)
		}
	}
	return filepath.Clean(edited) == filepath.Clean(path)
}

// revs is how many times the agent has edited a doc.
func revs(se core.SessionState, path string) int {
	n := 0
	for _, it := range se.Transcript {
		if it.Diff != nil && sameFile(se, it.Diff.Path, path) {
			n++
		}
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

func (x *planPlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "plan", Severity: core.SevError, Source: "plan", Text: err.Error()})
	}
}

type planView struct{ x *planPlugin }

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
func (v planView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "plan", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

type planDocsView struct{ x *planPlugin }

func (v planDocsView) Title(string) string { return "Plan docs" }
func (v planDocsView) EdgeToEdge()         {}
func (v planDocsView) Build(registry.ViewCtx) ui.Node {
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

// docsView is 05's left: the plan's docs with their threads, its source, its projects, and readiness.
func (x *planPlugin) docsView(c *ui.Ctx) {
	th := c.Theme()
	se, ok := x.front()
	c.Column("plandocs", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgSurface), Role: ui.RoleRegion, Label: "Plan docs",
		Help: "agent/plan#docs"}, func(c *ui.Ctx) {
		if !ok {
			noSessionNote(c)
			return
		}
		st := x.state(se.ID)
		docs := x.docs(se, st, c.Now())
		x.current(st, docs)
		c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, T: 12, B: 10}, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "PLAN DOCS", kickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d %s", len(docs), plural(len(docs), "file", "files")), monoFont(10, th.C(theme.FgFaint)))
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if len(docs) == 0 {
			c.Box("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 12)}, func(c *ui.Ctx) {
				widget.Wrap(c, "t", "No docs yet: the agent writes PLAN.md here.", 220, uiFont(12, th.C(theme.FgFaint)))
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
			c.Text("k", "SOURCE", kickerFont(th.C(theme.FgFaint)))
			if se.Ticket != "" {
				widget.Chip(c, "ticket", se.Ticket, th.C(theme.Accent), "agent/plan#docs")
			}
			prompt := se.Prompt
			if t, ok := se.Thread(se.PlanThread()); ok && prompt == "" {
				if cm, ok := firstComment(t); ok {
					prompt = cm.Body
				}
			}
			if prompt = strings.TrimSpace(plainMarkdown(prompt)); prompt != "" {
				widget.Wrap(c, "p", truncate(strings.Join(strings.Fields(prompt), " "), 160), 220, uiFont(12, th.C(theme.FgFaint)))
			}
		})
		if len(se.Projects) > 0 {
			c.Column("projects", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 4), Gap: 8, Role: ui.RoleGroup, Label: "Projects"}, func(c *ui.Ctx) {
				c.Text("k", "PROJECTS", kickerFont(th.C(theme.FgFaint)))
				for i, p := range se.Projects {
					c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						tone := th.C(theme.StatusQuiet)
						if p.ReadOnly {
							tone = th.C(theme.FgFaint)
						}
						c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: tone}, nil)
						c.Text("n", p.Name, monoFont(11.5, th.C(theme.Fg)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						c.Text("b", truncate(p.Branch, 24), monoFont(10.5, th.C(theme.FgFaint)))
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
					c.Text("k", "READINESS", kickerFont(th.C(theme.FgFaint)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("n", fmt.Sprintf("%d open", open), monoFont(10.5, th.C(theme.FgMuted)))
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
				widget.Wrap(c, "note", note, 220, uiFont(11.5, th.C(theme.FgFaint)))
			})
	})
}

// docRow is a doc in the list: its name, and its threads that need you, or are being worked on, or are open.
func (x *planPlugin) docRow(c *ui.Ctx, se core.SessionState, d planDoc, sel bool) {
	th := c.Theme()
	needs, working, open := 0, 0, 0
	for _, t := range docThreads(se, d) {
		switch groupOf(t) {
		case groupNeedsYou:
			needs++
		case groupWorking:
			working++
		case groupOpen:
			open++
		}
	}
	name := th.C(theme.FgMuted)
	if sel {
		name = th.C(theme.Fg)
	}
	c.Row("row", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Box("sq", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: th.C(tPlanMark)}, nil)
		c.Text("n", d.name, uiFont(13, name))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		switch {
		case needs > 0:
			widget.Chip(c, "n", fmt.Sprint(needs), th.C(theme.StatusNeedsYou), "agent/plan#docs")
		case working > 0:
			widget.Chip(c, "n", fmt.Sprint(working), th.C(theme.StatusWorking), "agent/plan#docs")
		case open > 0:
			widget.Chip(c, "n", fmt.Sprint(open), th.C(theme.FgFaint), "agent/plan#docs")
		default:
			c.Text("n", "–", monoFont(11, th.C(theme.FgGhost)))
		}
	})
}

// view is 05's main area: the doc in front rendered, its threads in the margin.
func (x *planPlugin) view(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	se, ok := x.front()
	root := c.Next("plan")
	c.Column("plan", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase), Role: ui.RoleRegion, Label: "Plan", Help: "agent/plan"}, func(c *ui.Ctx) {
		if !ok {
			noSessionNote(c)
			return
		}
		st := x.state(se.ID)
		docs := x.docs(se, st, c.Now())
		c.WakeAt(st.read.Add(time.Second))
		d, has := x.current(st, docs)
		x.header(c, se, d, has)
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if !has {
			c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 8}, func(c *ui.Ctx) {
				c.Text("k", "NO PLAN DOCS YET", kickerFont(th.C(tPlanMark)))
				c.Text("t", "The agent drafts PLAN.md at the workspace root; it shows here as it writes.", uiFont(13, th.C(theme.FgFaint)))
			})
			return
		}
		x.document(c, vc, se, st, d, root)
	})
}

func (x *planPlugin) header(c *ui.Ctx, se core.SessionState, d planDoc, has bool) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(48), Pad: ui.PadXY(20, 0), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		widget.Chip(c, "kind", "plan", th.C(tPlanMark), "agent/plan#header")
		title := se.Name
		for _, b := range d.blocks {
			if b.Kind == mdHeading && b.Level == 1 {
				title = plainMarkdown(b.Text)
				break
			}
		}
		c.Text("title", truncate(title, 64), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		var who []string
		if se.Agent.Provider != "" {
			who = append(who, strings.ToLower(providerName(se.Agent.Provider)))
		}
		if se.Agent.Model != "" {
			who = append(who, se.Agent.Model)
		}
		phase := "work started"
		if se.Planning() {
			phase = "drafting"
		}
		if has {
			phase += fmt.Sprintf(" rev %d", revs(se, d.path))
		}
		meta := phase
		if len(who) > 0 {
			meta = strings.Join(who, " ") + " · " + phase
		}
		c.Text("meta", meta, monoFont(10.5, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if doc := editing(se); doc != "" {
			c.Row("editing", ui.Style{Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.StatusWorking)}, nil)
				c.Text("t", "agent editing "+doc, monoFont(10.5, th.C(theme.FgMuted)))
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
func (x *planPlugin) document(c *ui.Ctx, vc registry.ViewCtx, se core.SessionState, st *planState, d planDoc, root ui.ID) {
	pr, ok := registry.PresenterNamed(x.h, "markdown")
	if !ok {
		c.Text("none", "No markdown presenter is registered.", uiFont(13, c.Theme().C(theme.FgFaint)))
		return
	}
	ps := st.present[d.name]
	if ps == nil {
		ps = newPresentState()
		st.present[d.name] = ps
	}
	if st.focus {
		ps.focus, st.focus = true, false
	}
	buf := st.bufs[d.name]
	if buf == nil || buf.Snapshot().String() != d.src {
		buf = text.New([]byte(d.src), text.Author{})
		st.bufs[d.name] = buf
	}
	kicker := fmt.Sprintf("%s · rev %d", d.name, revs(se, d.path))
	drawPresentation(c, x.h, x.sh, ps, presentation{presenter: pr, path: d.path, name: d.name, buf: buf, window: PlanView, kicker: kicker,
		focused: vc.Focused, root: root, source: func(line int) {
			x.run("editor.open", registry.Args{"path": d.path, "line": line + 1})
		}}, vc.Width)
}

// editedAgo is how long ago an edit was, to the second while it's fresh.
func editedAgo(now, t time.Time) string {
	if d := now.Sub(t); d < time.Minute {
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	}
	return ago(now, t) + " ago"
}

// planDocsArgs are view.open's args for the Plan docs panel: in the sidebar, alone and fixed.
func planDocsArgs() registry.Args {
	return sidebarArgs("view.open", registry.Args{"id": PlanDocsView, "place": "dock", "edge": "left", "size": 260, "title": "Plan docs"})
}
