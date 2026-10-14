package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

// ExplainKeyMode is the explain card's keys while it has focus: [ and ] widen and narrow, Escape closes it.
const ExplainKeyMode = "explain"

var (
	tExplainMark   = theme.Declare("explain.mark", "accent")
	tExplainMarkOn = theme.Declare("explain.mark.on", "accent.strong")
	tExplainUnitBg = theme.DeclareOn("explain.unit.bg", "alpha(accent, 5%)", "bg.base")
	tExplainEdge   = theme.Declare("explain.unit.edge", "alpha(accent, 70%)")
	tExplainCardBg = theme.DeclareOn("explain.card.bg", "alpha(accent, 4%)", "bg.base")
	tExplainCardBd = theme.Declare("explain.card.border", "alpha(accent, 35%)")
)

// explainDepths are the card's depths, in its strip's order.
var explainDepths = []string{registry.ExplainBrief, registry.ExplainDetailed, registry.ExplainWhy}

// explainExt is explain by code unit (ui.md 07b): ? marks on the units the branch changed, the explain card between
// the lines with its breadcrumb, [ and ] to widen and narrow, depths, a cache by the unit's content, Explain this on a
// selection and the right-click requests, and the Changed units panel. The answers come from the first
// registry.Explainer.
type explainExt struct {
	disposer
	sh *shell
	h  ext.Host

	parseMu sync.Mutex
	parsers map[string]*syntax.Parser // by language

	mu        sync.Mutex
	busy      sync.WaitGroup
	bufs      map[*text.Buffer]*fileUnits
	computing map[*text.Buffer]bool
	bases     map[string]*explainBase // by repo path
	answers   map[answerKey]*answer   // the cache: by a unit's content hash and the depth
	marks     []*explainMark
	panel     panelState

	card *explainCard // on the loop only
}

type answerKey struct{ hash, depth string }

type answer struct {
	text    string
	err     error
	loading bool
	by      string
}

// explainMark is a unit that was explained, followed through its buffer's edits, so that once its text changes its
// explanation reads "changed since".
type explainMark struct {
	path  string
	buf   *text.Buffer
	kind  string
	start text.AnchorID
	hash  string // the text it was explained as
}

// explainCrumb is a step of the card's breadcrumb: a unit, or the selection asked about.
type explainCrumb struct {
	label, kind string
	start, end  text.AnchorID
	selection   bool
	hash        string // the text its explanation is of
	context     string // more for the explainer, from explain.ask
}

// explainCard is the card between the lines: where it is, its breadcrumb and the step shown, and the depth.
type explainCard struct {
	path   string
	buf    *text.Buffer
	crumbs []explainCrumb // outermost first
	sel    int
	depth  int
	reveal bool
	units  int // how many units the breadcrumb has: the rest is the selection
}

func (cd *explainCard) rangeOf(i int) (text.Range, bool) {
	a, ok := cd.buf.Resolve(cd.crumbs[i].start)
	b, ok2 := cd.buf.Resolve(cd.crumbs[i].end)
	return text.Range{Start: a, End: max(a, b)}, ok && ok2
}

func (cd *explainCard) release() {
	for _, cr := range cd.crumbs {
		cd.buf.Release(cr.start)
		cd.buf.Release(cr.end)
	}
}

// breadcrumb is the card's crumbs up to the one shown: "func roundHalfEven › switch diff › case 0".
func (cd *explainCard) breadcrumb() string {
	var out []string
	for _, cr := range cd.crumbs[:cd.sel+1] {
		out = append(out, cr.label)
	}
	return strings.Join(out, " › ")
}

func (x *explainExt) Manifest() ext.Manifest { return ext.Manifest{ID: "explain"} }

func (x *explainExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.parsers, x.bufs, x.computing = map[string]*syntax.Parser{}, map[*text.Buffer]*fileUnits{}, map[*text.Buffer]bool{}
	x.bases, x.answers = map[string]*explainBase{}, map[answerKey]*answer{}
	if g := x.sh.git; g != nil && g.status != nil {
		x.add(disposeFn(g.status.Watch(func([]string) {
			x.mu.Lock()
			for _, b := range x.bases {
				b.loaded = false
			}
			x.mu.Unlock()
			h.Redraw()
		})))
	}
	cardOpen := func(registry.Args) bool { return x.card != nil }
	for _, c := range []registry.Command{
		{ID: "explain.unit", Title: "Explain the code unit at the cursor, or at {file}'s {line}; again widens to the unit around it",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.explainUnit(a) }},
		{ID: "explain.selection", Title: "Explain the Visual selection", Run: func(registry.Ctx, registry.Args) error { return x.explainSelection() }},
		{ID: "explain.this", Title: "Explain this: the selection, or the unit at the cursor",
			Run: func(_ registry.Ctx, a registry.Args) error {
				if strings.HasPrefix(x.sh.editor.e.Mode(), "visual") {
					return x.explainSelection()
				}
				return x.explainUnit(registry.Args{})
			}},
		{ID: "explain.ask", Title: "Explain something: {file}'s {line} through {end_line} (a card between its lines), or {text}; with {subject}, {kind}, {context} and {depth} (brief, detailed, why)",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.ask(a) }},
		{ID: "explain.widen", Title: "Explain the unit around the one explained", Available: cardOpen,
			Run: func(registry.Ctx, registry.Args) error { x.step(-1); return nil }},
		{ID: "explain.narrow", Title: "Explain the unit inside the one explained", Available: cardOpen,
			Run: func(registry.Ctx, registry.Args) error { x.step(1); return nil }},
		{ID: "explain.depth", Title: "Explain at a {depth}: brief, detailed, or why (with its history)", Available: cardOpen,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.setDepth(a.String("depth")) }},
		{ID: "explain.refresh", Title: "Explain the unit again, as it reads now", Available: cardOpen,
			Run: func(registry.Ctx, registry.Args) error { return x.refresh() }},
		{ID: "explain.follow-up", Title: "Ask a follow-up to the explanation: a new request with it attached", Available: cardOpen,
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.followUp(ctx) }},
		{ID: "explain.pin", Title: "Pin the explanation as a note: a resolved thread on its lines", Available: cardOpen,
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.pin(ctx) }},
		{ID: "explain.close", Title: "Close the explain card", Available: cardOpen,
			Run: func(registry.Ctx, registry.Args) error { x.closeCard(); return nil }},
		{ID: "explain.units", Title: "Show or hide the Changed units panel: every unit the branch touched, with its explain status",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "view.toggle", registry.Args{"id": ChangedUnitsView, "place": "dock", "edge": "right", "size": 350, "title": "Changed units"})
			}},
		{ID: "request.this", Title: "Write a request about the selection, or the unit at the cursor: {what} is review, tests or refactor",
			Available: func(registry.Args) bool { return h.Available("request.new", nil) },
			Run:       func(ctx registry.Ctx, a registry.Args) error { return x.requestThis(ctx, a.String("what")) }},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "n", Keys: "<Leader>e", Command: "explain.unit", Desc: "Explain the unit (again: wider)"},
		{Mode: "x", Keys: "<Leader>e", Command: "explain.selection", Desc: "Explain the selection"},
		{Mode: ExplainKeyMode, Keys: "[", Command: "explain.widen", Desc: "Wider", Nowait: true},
		{Mode: ExplainKeyMode, Keys: "]", Command: "explain.narrow", Desc: "Narrower", Nowait: true},
		{Mode: ExplainKeyMode, Keys: "<Esc>", Command: "explain.close", Desc: "Close the explanation"},
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.Contribute(registry.KeyLayer{Name: "explain.card", Mode: ExplainKeyMode, Active: x.focused, Source: "builtin:explain"}))
	x.add(h.Decorations("explain", registry.DecorationFunc(x.decorations)))
	for _, it := range []registry.MenuItem{
		{Name: "explain.this", Label: "Explain this", Key: "␣ e", Group: "agent", Order: 5, Command: "explain.this"},
		{Name: "request.review", Label: "Review this", Group: "agent", Order: 20, Command: "request.this", Args: registry.Args{"what": "review"}},
		{Name: "request.tests", Label: "Write tests for this", Group: "agent", Order: 30, Command: "request.this", Args: registry.Args{"what": "tests"}},
		{Name: "request.refactor", Label: "Refactor this…", Group: "agent", Order: 40, Command: "request.this", Args: registry.Args{"what": "refactor"}},
	} {
		it.When, it.Source = registry.MenuWhen{View: EditorView}, "builtin:explain"
		x.add(h.Contribute(it))
	}
	x.add(h.View(ChangedUnitsView, changedUnitsView{x}))
	x.add(h.Contribute(registry.EditorAction{Name: "explain.units", Label: "explain by unit", Command: "explain.units", Order: 60,
		Help: "editor/pane#explain", Source: "builtin:explain",
		When: func(path string) bool { _, n := x.changedIn(path); return n > 0 },
		On:   func(registry.EditorWindow) bool { return h.Layout().Find(layoutRef(ChangedUnitsView)) != nil },
		Note: func(w registry.EditorWindow) string {
			hunks, n := x.changedIn(w.Path)
			return fmt.Sprintf("%d changed %s · %d %s", n, plural(n, "unit", "units"), hunks, plural(hunks, "hunk", "hunks"))
		}}))
	return nil
}

func (x *explainExt) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	if x.card != nil {
		x.card.release()
		x.card = nil
	}
	x.parseMu.Lock()
	for _, p := range x.parsers {
		p.Close()
	}
	x.parseMu.Unlock()
	return x.disposer.Deactivate(ctx)
}

func (x *explainExt) wait() { x.busy.Wait() }

func (x *explainExt) notify(err error) {
	x.h.Notify(core.Notice{ID: "explain", Severity: core.SevWarning, Source: "explain", Key: true, Text: err.Error()})
}

// changedIn is how many hunks and changed units an open file has.
func (x *explainExt) changedIn(path string) (hunks, units int) {
	f, ok := x.sh.editor.openBuffer(path)
	if !ok {
		return 0, 0
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if fu := x.bufs[f.Text]; fu != nil {
		return fu.hunks, len(fu.owners)
	}
	return 0, 0
}

// focused is whether the card has the keys: it's open, and the editor's cursor is in normal mode inside what it explains.
func (x *explainExt) focused() bool {
	cd, ed := x.card, x.sh.editor
	if cd == nil || ed == nil || ed.e == nil || ed.e.Mode() != "normal" {
		return false
	}
	if f := x.h.Layout().Focus; f == nil || f.ID != EditorView {
		return false
	}
	p := ed.current()
	if p.Buffer() != cd.buf {
		return false
	}
	r, ok := cd.rangeOf(0)
	snap := p.Buf()
	cur := p.Cursor()
	if !ok || cur.Line >= snap.Lines() {
		return false
	}
	off := snap.LineStart(cur.Line) + min(cur.Col, len(snap.Line(cur.Line)))
	return r.Start <= off && off <= r.End
}

// firstNonBlank is the offset of a line's first character that isn't a blank.
func firstNonBlank(snap text.Snapshot, line int) int {
	l := snap.Line(line)
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return snap.LineStart(line) + i
}

// explainUnit explains the unit at {file}'s {line}, or at the cursor; at the cursor, again on the unit shown widens.
func (x *explainExt) explainUnit(a registry.Args) error {
	ed := x.sh.editor
	if ed == nil || ed.e == nil {
		return errors.New("no editor")
	}
	var path string
	var buf *text.Buffer
	at := 0
	if f := a.String("file"); f != "" {
		fs, ok := ed.openBuffer(f)
		if !ok {
			return fmt.Errorf("%s isn't open", filepath.Base(f))
		}
		path, buf = f, fs.Text
		snap := buf.Snapshot()
		at = firstNonBlank(snap, min(max(a.Int("line")-1, 0), snap.Lines()-1))
	} else {
		p := ed.current()
		if p.Name() == "" {
			return errors.New("the editor has no file here")
		}
		path, buf = ed.path(p), p.Buffer()
		snap, cur := p.Buf(), p.Cursor()
		at = max(snap.LineStart(cur.Line)+min(cur.Col, len(snap.Line(cur.Line))), firstNonBlank(snap, cur.Line))
		if cd := x.card; cd != nil && cd.buf == buf && cd.sel < cd.units {
			if r, ok := cd.rangeOf(cd.sel); ok && r.Start <= at && at <= r.End {
				x.step(-1)
				return nil
			}
		}
	}
	units := x.unitsNow(path, buf)
	i := text.Innermost(units, text.Range{Start: at, End: at})
	if i < 0 {
		return errors.New("no code unit here: select some code, and ␣e explains the selection")
	}
	x.openCard(path, buf, units, text.UnitPath(units, i), nil, "")
	return nil
}

// visualRange is the Visual selection in the current window, as a range of its text.
func visualRange(e *vim.Engine, p vim.Pane) (text.Range, bool) {
	start, ok := e.Visual()
	if !ok {
		return text.Range{}, false
	}
	end := p.Cursor()
	if end.Line < start.Line || end.Line == start.Line && end.Col < start.Col {
		start, end = end, start
	}
	snap := p.Buf()
	if e.Mode() != "visual" {
		return text.Range{Start: snap.LineStart(start.Line), End: snap.LineEnd(end.Line)}, true
	}
	a := snap.LineStart(start.Line) + min(start.Col, len(snap.Line(start.Line)))
	z := snap.LineStart(end.Line) + min(end.Col+1, len(snap.Line(end.Line)))
	return text.Range{Start: a, End: max(a, z)}, true
}

// explainSelection explains the Visual selection, inside the units around it, and leaves Visual mode.
func (x *explainExt) explainSelection() error {
	ed := x.sh.editor
	if ed == nil || ed.e == nil {
		return errors.New("no editor")
	}
	p := ed.current()
	r, ok := visualRange(ed.e, p)
	if !ok {
		return errors.New("nothing is selected")
	}
	ed.e.Key("<Esc>")
	return x.explainRange(ed.path(p), p.Buffer(), r, "", "")
}

// explainRange opens a card on a range of a buffer, under the units around it.
func (x *explainExt) explainRange(path string, buf *text.Buffer, r text.Range, subject, more string) error {
	units := x.unitsNow(path, buf)
	var around []int
	if i := text.Innermost(units, r); i >= 0 {
		around = text.UnitPath(units, i)
	}
	snap := buf.Snapshot()
	a, z := spanLines(snap, r)
	n := z - a + 1
	if subject == "" {
		subject = fmt.Sprintf("selection · %d %s", n, plural(n, "line", "lines"))
	}
	x.openCard(path, buf, units, around, &r, subject)
	x.card.crumbs[len(x.card.crumbs)-1].context = more
	x.ensure()
	return nil
}

// openCard opens the card on units (a chain of indexes into all), then the selection if there's one, showing the last.
func (x *explainExt) openCard(path string, buf *text.Buffer, all []text.Unit, chain []int, sel *text.Range, subject string) {
	x.closeCard()
	src := buf.Snapshot().Bytes()
	cd := &explainCard{path: path, buf: buf, reveal: true}
	for _, i := range chain {
		u := all[i]
		cd.crumbs = append(cd.crumbs, explainCrumb{label: u.Label(src), kind: u.Kind,
			start: buf.Anchor(u.Range.Start, text.Right), end: buf.Anchor(u.Range.End, text.Left)})
	}
	cd.units = len(cd.crumbs)
	if sel != nil {
		cd.crumbs = append(cd.crumbs, explainCrumb{label: subject, kind: "selection", selection: true,
			start: buf.Anchor(sel.Start, text.Right), end: buf.Anchor(sel.End, text.Left)})
	}
	cd.sel = len(cd.crumbs) - 1
	x.card = cd
	if sel == nil {
		x.ensure()
	}
}

func (x *explainExt) closeCard() {
	if x.card != nil {
		x.card.release()
		x.card = nil
	}
}

// step moves the card out (-1) or in (+1) along the syntax tree. In from the innermost unit it has, it goes into the
// unit inside that holds the cursor, else the first.
func (x *explainExt) step(d int) {
	cd := x.card
	if cd == nil {
		return
	}
	switch {
	case cd.sel+d >= 0 && cd.sel+d < len(cd.crumbs):
		cd.sel += d
	case d > 0 && cd.sel == cd.units-1 && cd.units == len(cd.crumbs):
		r, ok := cd.rangeOf(cd.sel)
		if !ok {
			return
		}
		units := x.unitsNow(cd.path, cd.buf)
		in := slices.IndexFunc(units, func(u text.Unit) bool { return u.Range == r && u.Kind == cd.crumbs[cd.sel].kind })
		if in < 0 {
			return
		}
		child := -1
		cursor := -1
		if p := x.sh.editor.current(); p.Buffer() == cd.buf {
			snap, cur := p.Buf(), p.Cursor()
			cursor = snap.LineStart(cur.Line) + min(cur.Col, len(snap.Line(cur.Line)))
		}
		for j, u := range units {
			if u.Parent != in {
				continue
			}
			if child < 0 || u.Range.Start <= cursor && cursor <= u.Range.End {
				child = j
			}
		}
		if child < 0 {
			return
		}
		u := units[child]
		cd.crumbs = append(cd.crumbs, explainCrumb{label: u.Label(cd.buf.Snapshot().Bytes()), kind: u.Kind,
			start: cd.buf.Anchor(u.Range.Start, text.Right), end: cd.buf.Anchor(u.Range.End, text.Left)})
		cd.units++
		cd.sel++
	default:
		return
	}
	cd.reveal = true
	x.ensure()
}

func (x *explainExt) setDepth(name string) error {
	i := slices.Index(explainDepths, name)
	if i < 0 {
		return fmt.Errorf("no depth %q: brief, detailed or why", name)
	}
	if x.card != nil {
		x.card.depth = i
		x.ensure()
	}
	return nil
}

// content is a crumb's text now, and its hash.
func (x *explainExt) content(cd *explainCard, i int) ([]byte, string, bool) {
	r, ok := cd.rangeOf(i)
	if !ok {
		return nil, "", false
	}
	b := cd.buf.Snapshot().Slice(r)
	return b, unitHash(cd.crumbs[i].kind, b), true
}

// changed is whether the step shown has changed since it was explained.
func (x *explainExt) changed(cd *explainCard) bool {
	_, h, ok := x.content(cd, cd.sel)
	return ok && cd.crumbs[cd.sel].hash != "" && h != cd.crumbs[cd.sel].hash
}

// shown is the answer for the step and depth shown.
func (x *explainExt) shown(cd *explainCard) *answer {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.answers[answerKey{cd.crumbs[cd.sel].hash, explainDepths[cd.depth]}]
}

// ensure asks about the step shown at its depth, unless the cache has it: an answer for the same text at the
// same depth.
func (x *explainExt) ensure() {
	cd := x.card
	if cd == nil {
		return
	}
	cr := &cd.crumbs[cd.sel]
	_, h, ok := x.content(cd, cd.sel)
	if !ok {
		return
	}
	if cr.hash == "" {
		cr.hash = h
		if r, ok := cd.rangeOf(cd.sel); ok {
			if old := x.markedAs(cd.buf, cr.kind, r.Start); old != "" {
				cr.hash = old // explained before an edit inside it: shown as changed since, with a refresh
			}
		}
	}
	if x.shown(cd) != nil {
		return
	}
	cr.hash = h
	if x.shown(cd) == nil {
		x.request(cd, cd.sel)
	}
}

// markedAs is the text hash a unit starting at off was last explained as.
func (x *explainExt) markedAs(buf *text.Buffer, kind string, off int) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, m := range x.marks {
		if o, ok := m.buf.Resolve(m.start); ok && m.buf == buf && m.kind == kind && o == off {
			return m.hash
		}
	}
	return ""
}

func (x *explainExt) refresh() error {
	if cd := x.card; cd != nil {
		_, h, _ := x.content(cd, cd.sel)
		cd.crumbs[cd.sel].hash = h
		x.ensure()
	}
	return nil
}

// spanLines are the first and last lines a range covers; an end in the blanks that start a line leaves that line out.
func spanLines(snap text.Snapshot, r text.Range) (int, int) {
	a, end := snap.PosAt(r.Start), snap.PosAt(r.End)
	z := end.Line
	if z > a.Line && strings.TrimSpace(string(snap.Line(z)[:min(end.Col, len(snap.Line(z)))])) == "" {
		z--
	}
	return a.Line, z
}

// codeOf is a range of a buffer with its line numbers, for the explainer.
func codeOf(snap text.Snapshot, r text.Range) string {
	a, z := spanLines(snap, r)
	var b strings.Builder
	for l := a; l <= z && l < snap.Lines(); l++ {
		fmt.Fprintf(&b, "%4d  %s\n", l+1, strings.TrimRight(string(snap.Line(l)), "\r\n"))
	}
	return b.String()
}

// relName is a path as the work tree names it.
func (x *explainExt) relName(path string) string {
	if r, err := filepath.Rel(x.sh.editor.dir, path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return path
}

// request asks the first explainer about a step of the card, off the loop; the answer goes in the cache.
func (x *explainExt) request(cd *explainCard, i int) {
	cr := cd.crumbs[i]
	depth := explainDepths[cd.depth]
	key := answerKey{cr.hash, depth}
	r, ok := cd.rangeOf(i)
	if !ok {
		return
	}
	snap := cd.buf.Snapshot()
	first, last := spanLines(snap, r)
	lines := fmt.Sprintf("%s:%d-%d", x.relName(cd.path), first+1, last+1)
	req := registry.ExplainRequest{Kind: "unit", Depth: depth, Files: []string{cd.path},
		Subject: fmt.Sprintf("%s (%s)", strings.Join(crumbLabels(cd.crumbs[:i+1]), " › "), lines)}
	if cr.selection {
		req.Kind = "selection"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The code, %s:\n```\n%s```\n", lines, codeOf(snap, r))
	if cr.context != "" {
		b.WriteString("\n" + cr.context + "\n")
	}
	if i > 0 {
		if or, ok := cd.rangeOf(0); ok {
			fmt.Fprintf(&b, "\nIt's inside %s, which reads:\n```\n%s```\n", cd.crumbs[0].label, codeOf(snap, or))
		}
	}
	threads := ""
	if depth == registry.ExplainWhy {
		threads = x.threadsOn(cd.path, first, last)
	}
	a := &answer{loading: true}
	x.mu.Lock()
	x.answers[key] = a
	x.mu.Unlock()
	path, buf, kind, start := cd.path, cd.buf, cr.kind, r.Start
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if depth == registry.ExplainWhy {
			b.WriteString(x.history(ctx, path, snap, r) + threads)
		}
		req.Context = b.String()
		md, by, err := registry.Explain(ctx, x.h, req)
		label := by.Name
		if by.Label != nil {
			registry.Call(func() error { label = by.Label(); return nil })
		}
		x.h.Do(func() {
			x.mu.Lock()
			a.text, a.err, a.loading, a.by = md, err, false, label
			if err == nil {
				x.marks = slices.DeleteFunc(x.marks, func(m *explainMark) bool {
					off, ok := m.buf.Resolve(m.start)
					if m.buf == buf && m.kind == kind && (!ok || off == start) {
						m.buf.Release(m.start)
						return true
					}
					return false
				})
				x.marks = append(x.marks, &explainMark{path: path, buf: buf, kind: kind, start: buf.Anchor(start, text.Right), hash: key.hash})
			}
			x.mu.Unlock()
			x.h.Redraw()
		})
	}()
}

func crumbLabels(cs []explainCrumb) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.label)
	}
	return out
}

// history is why · history's context: the commits that last touched the lines (blame), and the session's PR.
func (x *explainExt) history(ctx context.Context, path string, snap text.Snapshot, r text.Range) string {
	var b strings.Builder
	if g := x.sh.git; g != nil && g.status != nil {
		if blame, err := g.status.Repo().Blame(ctx, g.rel(path), snap.Bytes()); err == nil {
			a, z := spanLines(snap, r)
			seen := map[string]bool{}
			for l := a; l <= z && l < len(blame); l++ {
				bl := blame[l]
				key := bl.Hash
				if key == "" {
					key = "uncommitted"
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				if b.Len() == 0 {
					b.WriteString("\nHistory (git blame of these lines):\n")
				}
				if bl.Hash == "" {
					b.WriteString("- not committed yet\n")
					continue
				}
				fmt.Fprintf(&b, "- %s %s, %s (%s): %s\n", bl.Hash[:min(8, len(bl.Hash))], bl.Author, bl.When.Format("2006-01-02"), bl.Kind, bl.Summary)
			}
		}
	}
	return b.String()
}

// threadsOn is the session's threads on lines [a, z] of path, and its PR, for why · history.
func (x *explainExt) threadsOn(path string, a, z int) string {
	st := x.h.State()
	s, ok := st.Session(st.Focus)
	if !ok {
		return ""
	}
	se := s.Snapshot()
	var b strings.Builder
	if se.PR != "" {
		fmt.Fprintf(&b, "\nThe session's pull request: %s\n", se.PR)
	}
	for _, t := range se.Threads {
		if t.Anchor == nil || filepath.Clean(anchorPath(se, t.Anchor)) != filepath.Clean(path) {
			continue
		}
		l := t.Anchor.Line - 1
		if l < a || l > z {
			continue
		}
		fmt.Fprintf(&b, "\nA thread on line %d (%s): %s", t.Anchor.Line, t.Status, t.Heading())
		if last, ok := t.Last(); ok {
			fmt.Fprintf(&b, "\n  last, from %s: %s", last.Author, firstLine(last.Body))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ask is explain.ask: a card on {file}'s lines, or {text} answered in a notice.
func (x *explainExt) ask(a registry.Args) error {
	depth := cmpOr(a.String("depth"), registry.ExplainBrief)
	if !slices.Contains(explainDepths, depth) {
		return fmt.Errorf("no depth %q: brief, detailed or why", depth)
	}
	if f := a.String("file"); f != "" {
		line := max(a.Int("line"), 1)
		fs, ok := x.sh.editor.openBuffer(f)
		if !ok {
			if err := x.h.Run(context.Background(), "editor.open", registry.Args{"path": f, "line": line}); err != nil {
				return err
			}
			if fs, ok = x.sh.editor.openBuffer(f); !ok {
				return fmt.Errorf("couldn't open %s", f)
			}
		}
		snap := fs.Text.Snapshot()
		from := min(line-1, snap.Lines()-1)
		to := min(max(a.Int("end_line")-1, from), snap.Lines()-1)
		more := a.String("context")
		if k := a.String("kind"); k != "" {
			more = strings.TrimSpace("It's a " + k + ".\n" + more)
		}
		if err := x.explainRange(f, fs.Text, text.Range{Start: snap.LineStart(from), End: snap.LineEnd(to)}, a.String("subject"), more); err != nil {
			return err
		}
		x.card.depth = slices.Index(explainDepths, depth)
		x.ensure()
		return nil
	}
	body := a.String("text")
	if strings.TrimSpace(body) == "" {
		return errors.New("explain.ask needs a file and line, or text")
	}
	req := registry.ExplainRequest{Kind: cmpOr(a.String("kind"), "selection"), Subject: cmpOr(a.String("subject"), "this text"), Depth: depth,
		Context: strings.TrimSpace(body + "\n\n" + a.String("context"))}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		md, _, err := registry.Explain(ctx, x.h, req)
		x.h.Do(func() {
			if err != nil {
				x.notify(err)
				return
			}
			x.h.Notify(core.Notice{ID: "explain.answer", Severity: core.SevInfo, Source: "explain", Title: "Explain: " + req.Subject, Text: md})
		})
	}()
	return nil
}

// followUp opens a new request with the explanation and the code attached.
func (x *explainExt) followUp(ctx registry.Ctx) error {
	cd := x.card
	r, ok := cd.rangeOf(cd.sel)
	if !ok {
		return errors.New("the explained code is gone")
	}
	snap := cd.buf.Snapshot()
	ctxt := fmt.Sprintf("%s in %s\n```\n%s```", cd.breadcrumb(), x.relName(cd.path), codeOf(snap, r))
	if a := x.shown(cd); a != nil && a.text != "" {
		ctxt += "\n\nExplained as:\n" + a.text
	}
	return x.h.Run(ctx, "request.new", registry.Args{"text": "", "context": ctxt, "file": cd.path, "line": snap.PosAt(r.Start).Line + 1})
}

// pin makes the explanation a resolved thread on the lines it's about: a note that doesn't go to the agent.
func (x *explainExt) pin(ctx registry.Ctx) error {
	cd := x.card
	a := x.shown(cd)
	if a == nil || a.text == "" {
		return errors.New("there's no explanation to pin yet")
	}
	r, ok := cd.rangeOf(cd.sel)
	if !ok {
		return errors.New("the explained code is gone")
	}
	snap := cd.buf.Snapshot()
	from, to := spanLines(snap, r)
	body := a.text
	if a.by != "" {
		body += "\n\n— " + a.by
	}
	return x.h.Run(ctx, "request.comment", registry.Args{"file": cd.path, "line": from + 1, "end_line": to + 1,
		"text": strings.TrimRight(string(snap.Line(from)), "\r\n"), "title": "Explain: " + cd.breadcrumb(), "body": body,
		"status": string(core.ThreadResolved)})
}

// requestThis writes a request about the selection, or the unit at the cursor.
func (x *explainExt) requestThis(ctx registry.Ctx, what string) error {
	ed := x.sh.editor
	if ed == nil || ed.e == nil {
		return errors.New("no editor")
	}
	p := ed.current()
	if p.Name() == "" {
		return errors.New("the editor has no file here")
	}
	path, snap := ed.path(p), p.Buf()
	r, ok := visualRange(ed.e, p)
	name := "this code"
	if ok {
		ed.e.Key("<Esc>")
	} else {
		units := x.unitsNow(path, p.Buffer())
		cur := p.Cursor()
		i := text.Innermost(units, text.Range{Start: firstNonBlank(snap, cur.Line), End: firstNonBlank(snap, cur.Line)})
		if i < 0 {
			return errors.New("no code unit here: select some code first")
		}
		r, name = units[i].Range, units[i].Label(snap.Bytes())
	}
	start, end := spanLines(snap, r)
	where := fmt.Sprintf("%s:%d-%d", x.relName(path), start+1, end+1)
	prompt, ok := map[string]string{
		"review":   "Review %s (%s): what's wrong with it, or could be better?",
		"tests":    "Write tests for %s (%s).",
		"refactor": "Refactor %s (%s): ",
	}[what]
	if !ok {
		return fmt.Errorf("no request %q: review, tests or refactor", what)
	}
	return x.h.Run(ctx, "request.new", registry.Args{"text": fmt.Sprintf(prompt, name, where),
		"context": fmt.Sprintf("%s\n```\n%s```", where, codeOf(snap, r)), "file": path, "line": start + 1})
}

// decorations put a ? beside each unit the branch changed, and the card between the lines under what it explains,
// with those lines marked.
func (x *explainExt) decorations(ctx registry.DecorationCtx) []registry.Decoration {
	if ctx.Buffer == nil {
		return nil
	}
	var out []registry.Decoration
	if fu := x.bufferUnits(ctx.Path, ctx.Buffer, ctx.FileBytes); fu != nil {
		for _, i := range fu.owners {
			u := fu.units[i]
			line := lineAt(fu.src, u.Range.Start)
			if line < ctx.Top || line >= ctx.Bottom {
				continue
			}
			st := x.unitStatus(ctx.Path, u, fu.hashes[i])
			tok := map[string]theme.TokenID{"open": tExplainMarkOn, "explained": tUnitsDone, "changed": tUnitsChanged}[st]
			if tok == 0 {
				tok = tExplainMark
			}
			word, _ := unitStatusWord(st)
			out = append(out, registry.Decoration{Kind: registry.Sign, Line: line, Text: "?", Style: tok.Name(), Priority: 15, Command: "explain.unit",
				Args: registry.Args{"file": ctx.Path, "line": line + 1}, Tooltip: "Explain " + u.Label(fu.src) + " · " + word})
		}
	}
	cd := x.card
	if cd == nil || cd.buf != ctx.Buffer {
		return out
	}
	r, ok := cd.rangeOf(cd.sel)
	if !ok || r.End > ctx.Snap.Len() {
		return out
	}
	a, z := spanLines(ctx.Snap, r)
	for l := max(a, ctx.Top); l <= z && l < ctx.Bottom; l++ {
		out = append(out, registry.Decoration{Kind: registry.LineBg, Line: l, Style: tExplainUnitBg.Name(), Edge: tExplainEdge.Name(), Priority: 5})
	}
	if z >= ctx.Top && z < ctx.Bottom {
		out = append(out, registry.Decoration{Kind: registry.Block, Line: z, Priority: 28, Reveal: cd.reveal,
			Node: &ui.Func{Key: "explain.card", Fn: func(c *ui.Ctx) { x.cardView(c, cd) }}})
		cd.reveal = false
	}
	return out
}

// cardView is the card: EXPLAIN and the breadcrumb, wider and narrower; the answer; the depths, who answered, Ask a
// follow-up and Pin as note (25-explain-unit).
func (x *explainExt) cardView(c *ui.Ctx, cd *explainCard) {
	if x.card != cd {
		return
	}
	th := c.Theme()
	width := float32(900)
	if r, ok := c.Rect(c.Next("wrap")); ok {
		width = min(r.Dx(), 900)
	}
	const help = "editor/pane#explain"
	run := func(cmd string, a registry.Args) {
		if err := x.h.Run(context.Background(), cmd, a); err != nil {
			x.notify(err)
		}
	}
	c.Row("wrap", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Column("card", ui.Style{W: ui.Px(width), Fill: th.C(tExplainCardBg), Stroke: th.C(tExplainCardBd), Border: 1, Radius: 8, Clip: true,
			Role: ui.RoleGroup, Label: "Explain " + cd.breadcrumb(), Help: help}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 6, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
				c.Text("t", "EXPLAIN", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
				c.Row("crumbs", ui.Style{Gap: 5, Cross: ui.Center, Clip: true, W: ui.Flex(1)}, func(c *ui.Ctx) {
					for i, cr := range cd.crumbs {
						if i > 0 {
							c.Text(fmt.Sprint("sep", i), "›", monoFont(11, th.C(theme.FgGhost)))
						}
						if widget.ToggleChip(c, i, cr.label, i == cd.sel, widget.ToggleChipOpts{Help: help, Label: "Explain " + cr.label}) != (i == cd.sel) {
							cd.sel = i
							x.ensure()
						}
					}
				})
				if widget.Link(c, "wider", "[ wider", help) {
					run("explain.widen", nil)
				}
				if widget.Link(c, "narrower", "] narrower", help) {
					run("explain.narrow", nil)
				}
				if widget.Link(c, "close", "✕", help) {
					run("explain.close", nil)
				}
			})
			c.Box("rule1", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tExplainCardBd)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 14, R: 14, T: 9, B: 10}, Gap: 8}, func(c *ui.Ctx) {
				a := x.shown(cd)
				switch {
				case a == nil || a.loading:
					c.Text("wait", "Asking…", monoFont(11, th.C(theme.FgFaint)))
				case a.err != nil:
					c.Text("err", a.err.Error(), monoFont(11, th.C(theme.Error)))
				default:
					markdownText(c, "md", a.text, width-28, uiFont(13, th.C(theme.FgMuted)))
				}
				if x.changed(cd) {
					c.Row("changed", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("t", "changed since it was explained", monoFont(10.5, th.C(tUnitsChanged)))
						if widget.Link(c, "refresh", "refresh", help) {
							run("explain.refresh", nil)
						}
					})
				}
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tExplainCardBd)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(36), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				depth := cd.depth
				if widget.Segmented(c, "depth", []string{"BRIEF", "DETAILED", "WHY · HISTORY"}, &depth, widget.SegmentedOpts{Help: help, Label: "Depth"}) {
					run("explain.depth", registry.Args{"depth": explainDepths[depth]})
				}
				by := ""
				if a := x.shown(cd); a != nil {
					by = a.by
				}
				c.Box("by", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", by, monoFont(10.5, th.C(theme.FgFaint))) })
				a := x.shown(cd)
				answered := a != nil && a.text != ""
				if widget.Button(c, "follow", "Ask a follow-up", widget.ButtonOpts{Help: help, Disabled: !x.h.Available("request.new", nil)}) {
					run("explain.follow-up", nil)
				}
				if widget.Button(c, "pin", "Pin as note", widget.ButtonOpts{Help: help, Disabled: !answered || !x.h.Available("request.comment", nil)}) {
					run("explain.pin", nil)
				}
			})
		})
	})
}
