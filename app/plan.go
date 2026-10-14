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
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Plan mode's views (ui.md 05): a plan doc rendered, with its threads in the margin, and the docs beside it with the
// plan's source, projects and readiness.
const (
	PlanView     = "plan"
	PlanDocsView = "plan.docs"
	PlanKeyMode  = "plan"
)

var (
	tPlanMark       = theme.Declare("plan.mark", "amber")
	tPlanLineOn     = theme.DeclareOn("plan.line.selected", "alpha(accent, 6%)", "bg.base")
	tPlanEdited     = theme.DeclareOn("plan.edited.bg", "alpha(green, 7%)", "bg.base")
	tPlanEditedEdge = theme.Declare("plan.edited.edge", "green")
	tPlanCodeBorder = theme.Declare("plan.code.border", "line.strong")
)

// planEditShown is how long the agent's latest edit to a doc shows as a diff.
const planEditShown = 10 * time.Minute

// planExt is AI-8: a plan session's docs, read as documents, with threads anchored to their lines, readiness, and
// Start work. It drives the requests and sessions through their commands.
type planExt struct {
	disposer
	sh *shell
	h  ext.Host
	by map[core.SessionID]*planState

	dialog   *startWork // the Start work dialog, while it's open
	startGen int        // which opening of it the worker's answers are for
}

// planState is one session's plan views: the doc in front, the selected block, what's being written.
type planState struct {
	doc     string // the doc in front, by its name at the workspace root
	docSel  int
	sel     int // the selected block, or -1
	draft   *planDraft
	replies map[core.ThreadID]*string
	scroll  scrollState
	gaps    map[string]float32 // the space above each margin card, as last laid out
	focus   bool               // give the doc focus next frame
	reveal  bool               // bring the selected block into view next frame

	docs []planDoc
	read time.Time // when the docs were last read
	seen int       // the transcript's length then
}

// planDraft is a thread being opened on a block of a doc.
type planDraft struct {
	doc   string
	block int
	text  string
	focus bool
}

// planDoc is a markdown file at the workspace root.
type planDoc struct {
	name, path string
	lines      []string
	blocks     []mdBlock
}

func (x *planExt) Manifest() ext.Manifest { return ext.Manifest{ID: "plan"} }

func (x *planExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.by = map[core.SessionID]*planState{}
	hasPlan := func(registry.Args) bool {
		se, ok := x.front()
		return ok && se.StartWork
	}
	for _, c := range []registry.Command{
		{ID: "plan.open", Title: "Open the plan of the session in front: its docs ({doc}, PLAN.md first), threads and readiness", Available: hasPlan,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.open(ctx, a.String("doc")) }},
		{ID: "plan.next", Title: "Plan: the next block of the doc", Run: func(registry.Ctx, registry.Args) error { x.move(1); return nil }},
		{ID: "plan.prev", Title: "Plan: the block before", Run: func(registry.Ctx, registry.Args) error { x.move(-1); return nil }},
		{ID: "plan.comment", Title: "Plan: open a thread on the selected block of the doc", Available: hasPlan,
			Run: func(registry.Ctx, registry.Args) error { x.comment(); return nil }},
		{ID: "plan.start-work", Title: "Start work on the plan in front: a dialog of how, which runs session.start-work",
			Available: func(registry.Args) bool { se, ok := x.front(); return ok && se.Planning() },
			Run:       func(registry.Ctx, registry.Args) error { return x.openStartWork() }},
		{ID: "plan.start-work.run", Title: "Start work as the Start work dialog says", Available: func(registry.Args) bool { return x.dialog != nil },
			Run: func(registry.Ctx, registry.Args) error { return x.runStartWork() }},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "j", Command: "plan.next", Desc: "Next block"},
		{Keys: "<Down>", Command: "plan.next", Desc: "Next block"},
		{Keys: "k", Command: "plan.prev", Desc: "Block before"},
		{Keys: "<Up>", Command: "plan.prev", Desc: "Block before"},
		{Keys: "c", Command: "plan.comment", Desc: "Comment on it"},
		{Keys: "<CR>", Command: "plan.comment", Desc: "Comment on it"},
	} {
		k.Mode = PlanKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: StartWorkKeyMode, Keys: "<D-CR>", Command: "plan.start-work.run", Desc: "Start work"}))
	x.add(h.View(OverlayPrefix+"plan.start-work", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "startwork", Fn: x.startWorkView}
	})))
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

func (x *planExt) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

func (x *planExt) state(id core.SessionID) *planState {
	st := x.by[id]
	if st == nil {
		st = &planState{sel: -1, replies: map[core.ThreadID]*string{}, gaps: map[string]float32{}}
		x.by[id] = st
	}
	return st
}

// openIfPlanning shows the plan when the window is on a session that's planning.
func (x *planExt) openIfPlanning() {
	se, ok := x.front()
	if !ok || !se.Planning() || filepath.Clean(x.sh.cfg.WorkDir) != filepath.Clean(sessionDir(se)) {
		return
	}
	if err := x.h.Run(context.Background(), "plan.open", nil); err != nil {
		x.h.Log().Warn("plan", "err", err)
	}
}

// open shows the plan's views: the doc in the main area, the docs docked at the left.
func (x *planExt) open(ctx context.Context, doc string) error {
	se, ok := x.front()
	if !ok {
		return fmt.Errorf("no session is in front")
	}
	st := x.state(se.ID)
	if doc != "" {
		st.doc, st.sel, st.draft = doc, -1, nil
	}
	shown := map[string]bool{}
	for _, v := range x.h.Layout().Visible() {
		shown[v.ID] = true
	}
	if !shown[PlanDocsView] {
		if err := x.h.Run(ctx, "view.open", registry.Args{"id": PlanDocsView, "place": "dock", "edge": "left", "size": 260, "title": "Plan docs"}); err != nil {
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
func (x *planExt) docs(se core.SessionState, st *planState, now time.Time) []planDoc {
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
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(se.Dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		src := string(b)
		st.docs = append(st.docs, planDoc{name: e.Name(), path: path, lines: strings.Split(strings.TrimSuffix(src, "\n"), "\n"), blocks: mdParse(src)})
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
func (x *planExt) current(st *planState, docs []planDoc) (planDoc, bool) {
	for i, d := range docs {
		if d.name == st.doc {
			st.docSel = i
			return d, true
		}
	}
	if len(docs) == 0 {
		return planDoc{}, false
	}
	st.doc, st.docSel, st.sel = docs[0].name, 0, -1
	return docs[0], true
}

// onDoc reports whether an anchor is on the doc.
func onDoc(a *core.Anchor, d planDoc) bool {
	return a != nil && a.Project == core.DocsProject && filepath.ToSlash(a.File) == d.name
}

// blockAt is the block holding a 0-based line, or the next one after it.
func (d planDoc) blockAt(line int) int {
	for i, b := range d.blocks {
		if line < b.End {
			return i
		}
	}
	return len(d.blocks) - 1
}

// docThread is a thread on a doc, and the block it's on now.
type docThread struct {
	t     core.Thread
	block int
	line  int // 1-based, where it is now
}

func docThreads(se core.SessionState, d planDoc) []docThread {
	var out []docThread
	for _, t := range se.Threads {
		if !onDoc(t.Anchor, d) || t.Btw {
			continue
		}
		line := anchoredLine(d.lines, t.Anchor)
		out = append(out, docThread{t: t, block: d.blockAt(line), line: line + 1})
	}
	slices.SortStableFunc(out, func(a, b docThread) int { return cmp.Compare(a.line, b.line) })
	return out
}

// docEdit is the agent's latest edit to a doc, where it still reads as the edit left it: the blocks it changed, and
// for a one-line block, the line it replaced.
type docEdit struct {
	at     time.Time
	blocks map[int]bool
	was    map[int]string
	first  int // the first block it changed, where it's labelled; -1 for none
}

func (x *planExt) lastEdit(se core.SessionState, d planDoc, now time.Time) docEdit {
	e := docEdit{blocks: map[int]bool{}, was: map[int]string{}, first: -1}
	var it *core.Item
	for i := len(se.Transcript) - 1; i >= 0; i-- {
		if df := se.Transcript[i].Diff; df != nil && sameFile(se, df.Path, d.path) {
			it = &se.Transcript[i]
			break
		}
	}
	if it == nil || now.Sub(it.Time) > planEditShown {
		return e
	}
	e.at = it.Time
	removed := ""
	for _, l := range it.Diff.Lines {
		switch l.Op {
		case "-":
			if removed == "" {
				removed = l.Text
			}
		case "+":
			if l.New > 0 && l.New <= len(d.lines) && d.lines[l.New-1] == l.Text {
				b := d.blockAt(l.New - 1)
				e.blocks[b] = true
				if e.first < 0 || b < e.first {
					e.first = b
				}
				if removed != "" && d.blocks[b].Line == d.blocks[b].End {
					e.was[b] = removed
				}
			}
			removed = ""
		default:
			removed = ""
		}
	}
	return e
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

func (x *planExt) move(d int) {
	se, ok := x.front()
	if !ok {
		return
	}
	st := x.state(se.ID)
	doc, ok := x.current(st, x.docs(se, st, time.Now()))
	if !ok || len(doc.blocks) == 0 {
		return
	}
	st.sel = min(max(st.sel+d, 0), len(doc.blocks)-1)
	st.reveal = true
}

// comment opens a thread on the selected block.
func (x *planExt) comment() {
	se, ok := x.front()
	if !ok {
		return
	}
	st := x.state(se.ID)
	doc, ok := x.current(st, x.docs(se, st, time.Now()))
	if !ok || len(doc.blocks) == 0 {
		return
	}
	st.sel = max(st.sel, 0)
	st.draft = &planDraft{doc: doc.name, block: st.sel, focus: true}
}

// send sends a draft as a new thread on its block's first line.
func (x *planExt) send(st *planState, d planDoc) {
	dr := st.draft
	body := strings.TrimSpace(dr.text)
	if body == "" || dr.block >= len(d.blocks) {
		return
	}
	b := d.blocks[dr.block]
	a := registry.Args{"body": body, "file": d.path, "line": b.Line, "text": d.lines[b.Line-1]}
	if b.End > b.Line {
		a["end_line"] = b.End
	}
	if err := x.h.Run(context.Background(), "request.comment", a); err != nil {
		x.h.Notify(core.Notice{ID: "plan", Severity: core.SevError, Source: "plan", Text: err.Error()})
		return
	}
	st.draft = nil
	st.focus = true
}

func (x *planExt) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.h.Notify(core.Notice{ID: "plan", Severity: core.SevError, Source: "plan", Text: err.Error()})
	}
}

type planView struct{ x *planExt }

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

type planDocsView struct{ x *planExt }

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
func (x *planExt) docsView(c *ui.Ctx) {
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
				st.doc, st.sel, st.draft = docs[st.docSel].name, -1, nil
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
func (x *planExt) docRow(c *ui.Ctx, se core.SessionState, d planDoc, sel bool) {
	th := c.Theme()
	needs, working, open := 0, 0, 0
	for _, t := range docThreads(se, d) {
		switch groupOf(t.t) {
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
func (x *planExt) view(c *ui.Ctx, vc registry.ViewCtx) {
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

func (x *planExt) header(c *ui.Ctx, se core.SessionState, d planDoc, has bool) {
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

// document is the doc as a page, its blocks numbered where threads are, and the margin of threads beside them.
func (x *planExt) document(c *ui.Ctx, vc registry.ViewCtx, se core.SessionState, st *planState, d planDoc, root ui.ID) {
	th := c.Theme()
	width := vc.Width
	body := c.Next("body")
	if r, ok := c.Rect(body); ok {
		width = r.Dx()
	}
	margin := min(max(width*.38, 300), 520)
	docW := min(max(width-margin-120, 280), 760)
	threads := docThreads(se, d)
	edit := x.lastEdit(se, d, c.Now())
	if vc.Focused && !c.FocusWithin(root) || st.focus {
		c.Focus(body)
		st.focus = false
	}
	blockIDs := map[int]ui.ID{}
	scrolled(c, "body", &st.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, KeyMode: PlanKeyMode, Role: ui.RoleList, Label: d.name,
		Help: "agent/plan#doc"}, ui.Style{}, func(c *ui.Ctx, _ func(string) ui.ID) {
		c.Row("page", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("doc", ui.Style{W: ui.Px(docW + 76), Pad: ui.Insets{L: 16, T: 28, B: 48}}, func(c *ui.Ctx) {
				for i, b := range d.blocks {
					blockIDs[i] = x.block(c, se, st, d, i, b, threads, edit, docW)
				}
			})
			c.Box("rule", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			x.margin(c, se, st, d, threads, blockIDs, margin)
		})
	})
	if id, ok := blockIDs[st.sel]; ok && st.reveal {
		c.ScrollIntoView(body, id)
		st.reveal = false
	}
}

// block is one block of the doc: clicking it selects it and opens a thread on it. It returns the ID of what's
// clicked, whose top a thread on it is level with.
func (x *planExt) block(c *ui.Ctx, se core.SessionState, st *planState, d planDoc, i int, b mdBlock, threads []docThread, edit docEdit, width float32) ui.ID {
	th := c.Theme()
	line, tone := 0, render.Color{}
	for _, t := range threads {
		if t.block != i {
			continue
		}
		if line == 0 {
			line = t.line
		}
		if t.t.Open() && tone == (render.Color{}) {
			_, tok := statusLabel(se, t.t)
			if groupOf(t.t) == groupOpen {
				tok = theme.Accent
			}
			tone = th.C(tok)
		}
	}
	sel := st.sel == i
	fill := render.Color{}
	switch {
	case edit.blocks[i]:
		fill, tone = th.C(tPlanEdited), th.C(tPlanEditedEdge)
	case sel:
		fill = th.C(tPlanLineOn)
	}
	if sel && line == 0 {
		line = b.Line
	}
	top := float32(6)
	if b.Kind == mdHeading && b.Level > 1 {
		top = 22
	}
	var id ui.ID
	c.Box(fmt.Sprint("b", i), ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: top}}, func(c *ui.Ctx) {
		id = c.Next("click")
		if widget.Clickable(c, "click", ui.Style{Dir: ui.Row, W: ui.Flex(1), Fill: fill, Selected: sel, Role: ui.RoleListItem, Label: plainMarkdown(b.Text),
			Help: "agent/plan#doc"}, func(c *ui.Ctx) {
			c.Box("n", ui.Style{W: ui.Px(40), Pad: ui.Insets{R: 10, T: 4}, Main: ui.End, Dir: ui.Row}, func(c *ui.Ctx) {
				if line > 0 {
					c.Text("t", fmt.Sprint(line), monoFont(10.5, th.C(theme.FgGhost)))
				}
			})
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: tone}, nil)
			pad := ui.Insets{L: 12, R: 12, T: 2, B: 2}
			if edit.first == i {
				pad.T, pad.B = 14, 8 // room for its label
			}
			c.Box("in", ui.Style{W: ui.Px(width + 24), Pad: pad}, func(c *ui.Ctx) {
				x.blockBody(c, se, d, i, b, edit, width)
			})
		}).Clicked {
			st.sel = i
			st.draft = &planDraft{doc: d.name, block: i, focus: true}
		}
		if edit.first == i {
			c.Box("edited", ui.Style{Float: &ui.Float{At: render.Point{X: 1, Y: 0}, Self: render.Point{X: 1, Y: .5}, Offset: render.Point{X: -8, Y: top}}},
				func(c *ui.Ctx) {
					c.Row("chip", ui.Style{Pad: ui.PadXY(8, 3), Gap: 6, Cross: ui.Center, Radius: 10, Fill: th.C(theme.BgBase), Stroke: theme.WithAlpha(th.C(tPlanEditedEdge), .5),
						Border: 1, Role: ui.RoleText, Label: "agent edited"}, func(c *ui.Ctx) {
						c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: th.C(tPlanEditedEdge)}, nil)
						c.Text("t", "AGENT EDITED "+strings.ToUpper(editedAgo(c.Now(), edit.at)), kickerFont(th.C(tPlanEditedEdge)))
					})
				})
		}
	})
	return id
}

// editedAgo is how long ago an edit was, to the second while it's fresh.
func editedAgo(now, t time.Time) string {
	if d := now.Sub(t); d < time.Minute {
		return fmt.Sprintf("%ds ago", max(int(d.Seconds()), 0))
	}
	return ago(now, t) + " ago"
}

// blockBody draws a block as a document does: headings large, a fence in a box, an edited line as what it was and
// what it is.
func (x *planExt) blockBody(c *ui.Ctx, se core.SessionState, d planDoc, i int, b mdBlock, edit docEdit, width float32) {
	th := c.Theme()
	body := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14.5}, Color: th.C(theme.Fg)}
	spans := mdSpans(b.Text)
	if was, ok := edit.was[i]; ok {
		spans = wordDiff(was, d.lines[b.Line-1], b)
	}
	switch b.Kind {
	case mdHeading:
		if b.Level == 1 {
			c.Column("h", ui.Style{Gap: 10, Pad: ui.Insets{B: 8}}, func(c *ui.Ctx) {
				if i == firstHeading(d) {
					c.Text("k", strings.ToUpper(d.name)+" · REV "+fmt.Sprint(revs(se, d.path)), kickerFont(th.C(tPlanMark)))
				}
				widget.Rich(c, "t", spans, width, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 28, Weight: typeset.Medium},
					Color: th.C(theme.Fg)}, widget.RichOpts{})
				var toc []string
				for _, o := range d.blocks {
					if o.Kind == mdHeading && o.Level == 2 {
						toc = append(toc, plainMarkdown(o.Text))
					}
				}
				if len(toc) > 0 && i == firstHeading(d) {
					c.Text("toc", strings.Join(toc, " · "), monoFont(10.5, th.C(theme.FgFaint)))
				}
			})
			return
		}
		c.Row("h", ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dash", ui.Style{W: ui.Px(14), H: ui.Px(2), Fill: th.C(tPlanMark)}, nil)
			widget.Rich(c, "t", spans, width-26, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 17, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)}, widget.RichOpts{})
		})
	case mdItem:
		indent := float32(b.Level) * 16
		c.Row("li", ui.Style{Gap: 10, Pad: ui.Insets{L: indent}}, func(c *ui.Ctx) {
			c.Text("mark", b.Mark, uiFont(14.5, th.C(theme.FgFaint)))
			widget.Rich(c, "t", spans, width-24-indent, body, widget.RichOpts{})
		})
	case mdCode:
		c.Column("code", ui.Style{W: ui.Px(width), Pad: ui.Pad(14), Gap: 3, Radius: 6, Stroke: th.C(tPlanCodeBorder), Border: 1, Fill: th.C(theme.BgSurface)},
			func(c *ui.Ctx) {
				if b.Lang != "" {
					c.Text("lang", strings.ToUpper(b.Lang), kickerFont(th.C(theme.FgFaint)))
				}
				for j, l := range b.Code {
					c.Text(j, strings.ReplaceAll(l, "\t", "    "), monoFont(12, th.C(theme.FgMuted)))
				}
			})
	case mdRule:
		c.Box("hr", ui.Style{W: ui.Px(width), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	default:
		widget.Rich(c, "p", spans, width, body, widget.RichOpts{})
	}
}

func firstHeading(d planDoc) int {
	return slices.IndexFunc(d.blocks, func(b mdBlock) bool { return b.Kind == mdHeading && b.Level == 1 })
}

// wordDiff is a one-line block's text with what the edit took out struck through, the words both share kept: the
// middle words that differ, old then new.
func wordDiff(was, now string, b mdBlock) []widget.Span {
	strip := func(s string) string {
		if b.Kind == mdItem {
			if m := listItem.FindStringSubmatch(s); m != nil {
				return m[3]
			}
		}
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "#"))
	}
	ow, nw := strings.Fields(strip(was)), strings.Fields(strip(now))
	p := 0
	for p < len(ow) && p < len(nw) && ow[p] == nw[p] {
		p++
	}
	s := 0
	for s < len(ow)-p && s < len(nw)-p && ow[len(ow)-1-s] == nw[len(nw)-1-s] {
		s++
	}
	var out []widget.Span
	add := func(words []string) {
		if len(words) > 0 {
			out = append(out, mdSpans(strings.Join(words, " ")+" ")...)
		}
	}
	add(nw[:p])
	if old := ow[p : len(ow)-s]; len(old) > 0 {
		out = append(out, widget.Span{Text: plainMarkdown(strings.Join(old, " ")), Strike: true}, widget.Span{Text: " "})
	}
	add(nw[p : len(nw)-s])
	add(nw[len(nw)-s:])
	return out
}

// margin is the threads on the doc, each level with its block (or under the one before it), and a thread being
// opened.
func (x *planExt) margin(c *ui.Ctx, se core.SessionState, st *planState, d planDoc, threads []docThread, blocks map[int]ui.ID, width float32) {
	th := c.Theme()
	type entry struct {
		key   string
		block int
		draw  func(c *ui.Ctx)
	}
	var entries []entry
	for _, t := range threads {
		t := t
		entries = append(entries, entry{key: "t." + string(t.t.ID), block: t.block, draw: func(c *ui.Ctx) { x.marginThread(c, se, st, t) }})
	}
	if dr := st.draft; dr != nil && dr.doc == d.name && dr.block < len(d.blocks) {
		entries = append(entries, entry{key: "draft", block: dr.block, draw: func(c *ui.Ctx) { x.draftCard(c, st, d) }})
		slices.SortStableFunc(entries, func(a, b entry) int { return cmp.Compare(a.block, b.block) })
	}
	id := c.Next("margin")
	c.Column("margin", ui.Style{W: ui.Px(width), Pad: ui.Insets{L: 20, R: 20, T: 28, B: 48}}, func(c *ui.Ctx) {
		col, ok := c.Rect(id)
		y := col.Min.Y + 28
		for _, e := range entries {
			gap := float32(0)
			if r, okB := c.Rect(blocks[e.block]); ok && okB {
				gap = max(r.Min.Y-y, 0)
			}
			gap = float32(int(gap))
			if old, seen := st.gaps[e.key]; !seen || old != gap {
				st.gaps[e.key] = gap
				c.WakeAt(c.Now()) // laid out against the blocks' places next frame
			}
			c.Box("gap."+e.key, ui.Style{H: ui.Px(gap)}, nil)
			card := c.Next(e.key)
			c.Box(e.key, ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 10}}, e.draw)
			if r, okC := c.Rect(card); okC {
				y += gap + r.Dy()
			} else {
				y += gap
			}
		}
		if len(entries) == 0 {
			c.Text("none", "Click a line to comment on it.", uiFont(12, th.C(theme.FgGhost)))
		}
	})
}

// marginThread is an open thread's card, or a closed one folded to a line.
func (x *planExt) marginThread(c *ui.Ctx, se core.SessionState, st *planState, t docThread) {
	th := c.Theme()
	if t.t.Open() {
		reply := st.replies[t.t.ID]
		if reply == nil {
			reply = new(string)
			st.replies[t.t.ID] = reply
		}
		inlineThreadCard(c, x.h, se, t.t, reply, func() {
			body := strings.TrimSpace(*reply)
			if body == "" {
				return
			}
			x.run("request.comment", registry.Args{"thread": string(t.t.ID), "body": body})
			*reply = ""
		}, threadCardOpts{Defer: se.Planning()})
		return
	}
	_, tok := statusLabel(se, t.t)
	c.Row("folded", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Radius: 6, Stroke: th.C(tReqThreadBd), Border: 1,
		Role: ui.RoleGroup, Label: "Thread " + t.t.Heading()}, func(c *ui.Ctx) {
		c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tok)}, nil)
		c.Text("l", fmt.Sprintf("L%d", t.line), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold}, Color: th.C(theme.FgMuted)})
		widget.Chip(c, "status", string(t.t.Status), th.C(tok), "agent/plan#threads")
		quote := "“" + firstLine(t.t.Heading()) + "”"
		if last, ok := t.t.Last(); ok && last.Author == core.AuthorAgent {
			quote += " → " + firstLine(plainMarkdown(last.Body))
		}
		if r, ok := c.Rect(c.Next("q")); ok {
			quote = truncate(quote, max(int(r.Dx()/6.4), 8))
		}
		c.Box("q", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", quote, uiFont(11.5, th.C(theme.FgFaint))) })
		if widget.Link(c, "reopen", "reopen", "agent/plan#threads") {
			x.run("request.reopen", registry.Args{"thread": string(t.t.ID)})
		}
	})
}

// draftCard is the card of a thread being opened on a block: ⏎ sends it, Escape drops it.
func (x *planExt) draftCard(c *ui.Ctx, st *planState, d planDoc) {
	th := c.Theme()
	dr := st.draft
	b := d.blocks[dr.block]
	inlineCard(c, "New thread", func(c *ui.Ctx, width float32) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.Accent)}, nil)
			c.Text("t", "New thread", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("l", fmt.Sprintf("· L%d", b.Line), monoFont(10.5, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("keys", "⏎ sends · esc drops it", monoFont(10, th.C(theme.FgGhost)))
		})
		c.Box("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 10}}, func(c *ui.Ctx) {
			id := c.Next("input")
			if dr.focus && c.Interaction(id).Focused {
				dr.focus = false
			} else if dr.focus {
				c.Focus(id)
			}
			r := widget.Input(c, "input", &dr.text, widget.InputOpts{Label: "Comment on this line", Placeholder: "Comment on " + d.name + ":" + fmt.Sprint(b.Line),
				Help: "agent/plan#threads"})
			switch {
			case r.Submitted:
				x.send(st, d)
			case r.Cancelled:
				st.draft, st.focus = nil, true
			}
		})
	})
}
