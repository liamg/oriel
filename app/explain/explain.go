package explain

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

// ExplainKeyMode is the explain card's keys while it has focus: [ and ] widen and narrow, Escape closes it.
const ExplainKeyMode = "explain"

var (
	tExplainMark      = theme.Declare("explain.mark", "accent")
	tExplainMarkOn    = theme.Declare("explain.mark.on", "accent.strong")
	tExplainMarkQuiet = theme.Declare("explain.mark.unchanged", "fg.ghost")
	tExplainUnitBg    = theme.DeclareOn("explain.unit.bg", "alpha(accent, 5%)", "bg.base")
	tExplainEdge      = theme.Declare("explain.unit.edge", "alpha(accent, 70%)")
	tExplainCardBg    = theme.DeclareOn("explain.card.bg", "alpha(accent, 4%)", "bg.base")
	tExplainCardBd    = theme.Declare("explain.card.border", "alpha(accent, 35%)")
)

// explainDepths are the card's depths, in its strip's order.
var explainDepths = []string{registry.ExplainBrief, registry.ExplainDetailed, registry.ExplainWhy}

// Plugin is explain by code unit: ? marks on the units the branch changed, the explain card between
// the lines with its breadcrumb, [ and ] to widen and narrow, depths, a cache by the unit's content, Explain this on a
// selection and the right-click requests, and the Changed units panel. The answers come from the first
// registry.Explainer.
type Plugin struct {
	kit.Disposer
	h plugin.Host

	parseMu sync.Mutex
	parsers map[string]*syntax.Parser // by language

	mu        sync.Mutex
	busy      sync.WaitGroup
	bufs      map[*text.Buffer]*fileUnits
	computing map[*text.Buffer]bool
	bases     map[string]*explainBase // by repo path
	answers   map[answerKey]*answer   // the cache: by a unit's content hash and the depth
	kept      map[string]*answer      // explain.ask's with a {key}, shown where they were asked
	marks     []*explainMark
	panel     panelState

	card *explainCard // on the loop only
}

type answerKey struct{ hash, depth string }

type answer struct {
	text    string
	err     error
	loading bool
	since   time.Time // when it was asked
	by      string
}

// state is a's load state for the widgets; nil is one not asked yet.
func (a *answer) state() widget.LoadState {
	switch {
	case a == nil:
		return widget.LoadState{Phase: widget.Idle}
	case a.loading:
		return widget.LoadState{Phase: widget.Pending, Since: a.since}
	case a.err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: a.err}
	}
	return widget.LoadState{Phase: widget.Ready}
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

func (x *Plugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "explain"} }

func (x *Plugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.parsers, x.bufs, x.computing = map[string]*syntax.Parser{}, map[*text.Buffer]*fileUnits{}, map[*text.Buffer]bool{}
	x.bases, x.answers, x.kept = map[string]*explainBase{}, map[answerKey]*answer{}, map[string]*answer{}
	x.Add(h.Contribute(registry.Service{Name: kit.ExplainService, Impl: x, Source: "builtin:explain"}))
	if st := kit.GitOf(h).Tree("").Status(); st != nil {
		x.Add(kit.OnDispose(st.Watch(func([]string) {
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
				if ed, ok := registry.EditorsOf(h); ok && strings.HasPrefix(ed.Mode(), "visual") {
					return x.explainSelection()
				}
				return x.explainUnit(registry.Args{})
			}},
		{ID: registry.ExplainCommand, Title: "Explain something: {file}'s {line} through {end_line} (a card between its lines), or {text} or {context}; with {subject}, {kind} and {depth} (brief, detailed, why); with a {key}, the answer is kept for where it was asked",
			Available: func(registry.Args) bool { return len(registry.Explainers(h)) > 0 },
			Run:       func(_ registry.Ctx, a registry.Args) error { return x.ask(a) }},
		{ID: registry.FixCommand, Title: "Ask the agent to fix something: a request with its {subject} and {context} attached, about {file}:{line}; {fix} is what it says",
			Available: func(registry.Args) bool { return h.Available("request.new", nil) },
			Run:       func(ctx registry.Ctx, a registry.Args) error { return x.fix(ctx, a) }},
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
		x.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "n", Keys: "<Leader>e", Command: "explain.unit", Desc: "Explain the unit (again: wider)"},
		{Mode: "x", Keys: "<Leader>e", Command: "explain.selection", Desc: "Explain the selection"},
		{Mode: ExplainKeyMode, Keys: "[", Command: "explain.widen", Desc: "Wider", Nowait: true},
		{Mode: ExplainKeyMode, Keys: "]", Command: "explain.narrow", Desc: "Narrower", Nowait: true},
		{Mode: ExplainKeyMode, Keys: "<Esc>", Command: "explain.close", Desc: "Close the explanation"},
	} {
		x.Add(h.Keymap(k))
	}
	x.Add(h.Contribute(registry.KeyLayer{Name: "explain.card", Mode: ExplainKeyMode, Active: x.focused, Source: "builtin:explain"}))
	x.Add(h.Decorations("explain", uireg.KeyedDecorations{Key: x.decorationKey, Make: x.decorations}))
	for _, it := range []registry.MenuItem{
		{Name: "explain.this", Label: "Explain this", Key: "␣ e", Group: "agent", Order: 5, Command: "explain.this"},
		{Name: "request.review", Label: "Review this", Group: "agent", Order: 20, Command: "request.this", Args: registry.Args{"what": "review"}},
		{Name: "request.tests", Label: "Write tests for this", Group: "agent", Order: 30, Command: "request.this", Args: registry.Args{"what": "tests"}},
		{Name: "request.refactor", Label: "Refactor this…", Group: "agent", Order: 40, Command: "request.this", Args: registry.Args{"what": "refactor"}},
	} {
		it.When, it.Source = registry.MenuWhen{View: kit.EditorView}, "builtin:explain"
		x.Add(h.Contribute(it))
	}
	x.Add(h.View(ChangedUnitsView, changedUnitsView{x}))
	x.Add(h.Contribute(registry.EditorAction{Name: "explain.units", Label: "explain by unit", Command: "explain.units", Order: 60,
		Help: "editor/pane#explain", Source: "builtin:explain",
		When: func(path string) bool { _, n := x.changedIn(path); return n > 0 },
		On:   func(registry.EditorWindow) bool { return h.Layout().Find(layout.ViewRef{ID: ChangedUnitsView}) != nil },
		Note: func(w registry.EditorWindow) string {
			hunks, n := x.changedIn(w.Path)
			return fmt.Sprintf("%d changed %s · %d %s", n, kit.Plural(n, "unit", "units"), hunks, kit.Plural(hunks, "hunk", "hunks"))
		}}))
	return nil
}

func (x *Plugin) Deactivate(ctx context.Context) error {
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
	return x.Disposer.Deactivate(ctx)
}

// Wait waits for the units, bases and answers being worked out off the loop.
func (x *Plugin) Wait() { x.busy.Wait() }

func (x *Plugin) notify(err error) {
	x.h.Notify(core.Notice{ID: "explain", Severity: core.SevWarning, Source: "explain", Key: true, Text: err.Error()})
}

// changedIn is how many hunks and changed units an open file has.
func (x *Plugin) changedIn(path string) (hunks, units int) {
	ed, ok := registry.EditorsOf(x.h)
	if !ok {
		return 0, 0
	}
	f, ok := ed.Buffer(path)
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
func (x *Plugin) focused() bool {
	cd := x.card
	ed, ok := registry.EditorsOf(x.h)
	if cd == nil || !ok || ed.Mode() != "normal" {
		return false
	}
	b, ok := ed.Focused()
	if !ok || b.Text != cd.buf {
		return false
	}
	r, ok := cd.rangeOf(0)
	snap := b.Text.Snapshot()
	cur := b.Cursor
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
func (x *Plugin) explainUnit(a registry.Args) error {
	ed, ok := registry.EditorsOf(x.h)
	if !ok {
		return errors.New("no editor")
	}
	var path string
	var buf *text.Buffer
	at := 0
	if f := a.String("file"); f != "" {
		fs, ok := ed.Buffer(f)
		if !ok {
			return fmt.Errorf("%s isn't open", filepath.Base(f))
		}
		path, buf = f, fs.Text
		snap := buf.Snapshot()
		at = firstNonBlank(snap, min(max(a.Int("line")-1, 0), snap.Lines()-1))
	} else {
		b, ok := ed.Current()
		if !ok || b.Path == "" {
			return errors.New("the editor has no file here")
		}
		path, buf = b.Path, b.Text
		snap, cur := buf.Snapshot(), b.Cursor
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

// current is the editor's current window's buffer.
func (x *Plugin) current() (registry.EditorBuffer, bool) {
	ed, ok := registry.EditorsOf(x.h)
	if !ok {
		return registry.EditorBuffer{}, false
	}
	return ed.Current()
}

// explainSelection explains the Visual selection, inside the units around it, and leaves Visual mode.
func (x *Plugin) explainSelection() error {
	ed, ok := registry.EditorsOf(x.h)
	if !ok {
		return errors.New("no editor")
	}
	b, _ := ed.Current()
	r, ok := ed.Selection()
	if !ok {
		return errors.New("nothing is selected")
	}
	x.leaveVisual()
	return x.explainRange(b.Path, b.Text, r, "", "", "")
}

// leaveVisual takes the editor out of Visual mode, as <Esc> does.
func (x *Plugin) leaveVisual() {
	x.h.Run(context.Background(), "editor.keys", registry.Args{"keys": "<Esc>", "noremap": true})
}

// explainRange opens a card on a range of a buffer, under the units around it; kind is what it is, a selection if "".
func (x *Plugin) explainRange(path string, buf *text.Buffer, r text.Range, kind, subject, more string) error {
	units := x.unitsNow(path, buf)
	var around []int
	if i := text.Innermost(units, r); i >= 0 {
		around = text.UnitPath(units, i)
	}
	snap := buf.Snapshot()
	a, z := spanLines(snap, r)
	n := z - a + 1
	if subject == "" {
		subject = fmt.Sprintf("selection · %d %s", n, kit.Plural(n, "line", "lines"))
	}
	x.openCard(path, buf, units, around, &r, subject)
	last := &x.card.crumbs[len(x.card.crumbs)-1]
	last.context, last.kind = more, cmp.Or(kind, "selection")
	x.ensure()
	return nil
}

// openCard opens the card on units (a chain of indexes into all), then the selection if there's one, showing the last.
func (x *Plugin) openCard(path string, buf *text.Buffer, all []text.Unit, chain []int, sel *text.Range, subject string) {
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

func (x *Plugin) closeCard() {
	if x.card != nil {
		x.card.release()
		x.card = nil
	}
}

// step moves the card out (-1) or in (+1) along the syntax tree. In from the innermost unit it has, it goes into the
// unit inside that holds the cursor, else the first.
func (x *Plugin) step(d int) {
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
		if b, ok := x.current(); ok && b.Text == cd.buf {
			snap, cur := b.Text.Snapshot(), b.Cursor
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

func (x *Plugin) setDepth(name string) error {
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
func (x *Plugin) content(cd *explainCard, i int) ([]byte, string, bool) {
	r, ok := cd.rangeOf(i)
	if !ok {
		return nil, "", false
	}
	b := cd.buf.Snapshot().Slice(r)
	return b, unitHash(cd.crumbs[i].kind, b), true
}

// changed is whether the step shown has changed since it was explained.
func (x *Plugin) changed(cd *explainCard) bool {
	_, h, ok := x.content(cd, cd.sel)
	return ok && cd.crumbs[cd.sel].hash != "" && h != cd.crumbs[cd.sel].hash
}

// shown is the answer for the step and depth shown.
func (x *Plugin) shown(cd *explainCard) *answer {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.answers[answerKey{cd.crumbs[cd.sel].hash, explainDepths[cd.depth]}]
}

// ensure asks about the step shown at its depth, unless the cache has it: an answer for the same text at the
// same depth.
func (x *Plugin) ensure() {
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
func (x *Plugin) markedAs(buf *text.Buffer, kind string, off int) string {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, m := range x.marks {
		if o, ok := m.buf.Resolve(m.start); ok && m.buf == buf && m.kind == kind && o == off {
			return m.hash
		}
	}
	return ""
}

func (x *Plugin) refresh() error {
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
func (x *Plugin) relName(path string) string {
	ed, ok := registry.EditorsOf(x.h)
	if !ok {
		return path
	}
	if r, err := filepath.Rel(ed.Dir(), path); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return path
}
