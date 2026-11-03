package layout

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/snapshot"
)

var (
	code  = ViewRef{ID: "editor", Instance: "round.go"}
	test  = ViewRef{ID: "editor", Instance: "round_test.go"}
	term  = ViewRef{ID: "terminal", Instance: "1"}
	probs = ViewRef{ID: "problems"}
	tree  = ViewRef{ID: "explorer"}
	reqs  = ViewRef{ID: "requests"}
)

// start is a typical arrangement: two files in main, the explorer docked left, problems and a
// terminal in the bottom dock.
func start(t *testing.T) *Root {
	t.Helper()
	r := New()
	for _, op := range []struct {
		v ViewRef
		p Placement
		o Opts
	}{
		{code, Tab, Opts{}}, {test, Tab, Opts{}}, {tree, Docked, Opts{Edge: Left}},
		{probs, Docked, Opts{Edge: Bottom}}, {term, Docked, Opts{Edge: Bottom}},
	} {
		if err := r.Open(op.v, op.p, op.o); err != nil {
			t.Fatal(err)
		}
	}
	r.Focus = &code
	r.Main.Active = 0
	return r
}

// Every move and drop target, each from the same start, pinned as the tree it leaves.
func TestMovesAndDrops(t *testing.T) {
	steps := []struct {
		name string
		do   func(r *Root) error
	}{
		{"start", func(r *Root) error { return nil }},
		{"⌃W J: dock the terminal at the bottom (already there)", func(r *Root) error { return r.Move(term, Docked, Opts{Edge: Bottom}) }},
		{"⌃W L: split the terminal right of the focused file", func(r *Root) error { return r.Move(term, Split, Opts{Side: SideRight}) }},
		{"split the terminal below", func(r *Root) error { return r.Move(term, Split, Opts{Side: SideDown}) }},
		{"split the terminal left", func(r *Root) error { return r.Move(term, Split, Opts{Side: SideLeft}) }},
		{"split the terminal above", func(r *Root) error { return r.Move(term, Split, Opts{Side: SideUp}) }},
		{"⌃W T: the terminal as a tab in main", func(r *Root) error { return r.Move(term, Tab, Opts{}) }},
		{"⌃W F: float the terminal", func(r *Root) error { return r.Move(term, Floating, Opts{}) }},
		{"⌃`: the terminal in the drop-down", func(r *Root) error { return r.Move(term, DropDown, Opts{}) }},
		{"dock the explorer right", func(r *Root) error { return r.Move(tree, Docked, Opts{Edge: Right}) }},
		{"drop on a leaf's middle: a tab there", func(r *Root) error { return r.Drop(probs, Target{Beside: &code, Zone: ZoneCenter}) }},
		{"drop on a leaf's left edge", func(r *Root) error { return r.Drop(term, Target{Beside: &code, Zone: ZoneLeft}) }},
		{"drop on a leaf's right edge", func(r *Root) error { return r.Drop(term, Target{Beside: &code, Zone: ZoneRight}) }},
		{"drop on a leaf's top edge", func(r *Root) error { return r.Drop(term, Target{Beside: &code, Zone: ZoneTop}) }},
		{"drop on a leaf's bottom edge", func(r *Root) error { return r.Drop(term, Target{Beside: &code, Zone: ZoneBottom}) }},
		{"drop a tab on its own leaf's edge: splits it off", func(r *Root) error { return r.Drop(test, Target{Beside: &test, Zone: ZoneRight}) }},
		{"drop a lone tab on its own leaf's edge: nothing", func(r *Root) error { return r.Drop(term, Target{Beside: &term, Zone: ZoneRight}) }},
		{"drop on the left dock", func(r *Root) error { return r.Drop(probs, Target{Dock: Left}) }},
		{"drop on the right dock", func(r *Root) error { return r.Drop(probs, Target{Dock: Right}) }},
		{"drop in open space: a float there", func(r *Root) error {
			f := render.XYWH(300, 200, 500, 300)
			return r.Drop(term, Target{Float: &f})
		}},
		{"split twice the same way: one split, three children", func(r *Root) error {
			r.Move(term, Split, Opts{Side: SideRight})
			return r.Move(probs, Split, Opts{Side: SideRight, Beside: &term})
		}},
		{"moving the last view out of a split collapses it", func(r *Root) error {
			r.Move(term, Split, Opts{Side: SideRight})
			return r.Move(term, Docked, Opts{Edge: Bottom})
		}},
		{"close the focused tab: focus goes to its neighbour", func(r *Root) error { r.Close(code); return nil }},
		{"⌃J: close the bottom dock", func(r *Root) error { r.ToggleDock(Bottom); return nil }},
		{"⌃W =: even out the splits", func(r *Root) error {
			r.Move(term, Split, Opts{Side: SideRight})
			r.Main.Weights = []float32{3, 1}
			r.Equalize()
			return nil
		}},
		{"⌃W Z: zoom the terminal", func(r *Root) error {
			r.Move(term, Split, Opts{Side: SideRight})
			r.ToggleZoom(term)
			return nil
		}},
		{"⌃W Z again: the exact tree comes back", func(r *Root) error {
			r.Move(term, Split, Opts{Side: SideRight})
			before := r.Dump()
			r.ToggleZoom(term)
			r.ToggleZoom(term)
			if r.Dump() != before {
				return errorf("zoom didn't restore:\n%s\nwas:\n%s", r.Dump(), before)
			}
			return nil
		}},
		{"open something new as a split", func(r *Root) error { return r.Open(reqs, Split, Opts{Side: SideRight}) }},
	}
	var b strings.Builder
	for _, s := range steps {
		r := start(t)
		if err := s.do(r); err != nil {
			t.Errorf("%s: %v", s.name, err)
		}
		b.WriteString("=== " + s.name + "\n" + r.Dump())
	}
	snapshot.Golden(t, "testdata/moves.golden", []byte(b.String()))
}

type errString string

func (e errString) Error() string { return string(e) }

func errorf(f string, args ...any) error { return errString(fmt.Sprintf(f, args...)) }

func TestSaveAndRestore(t *testing.T) {
	r := start(t)
	r.Move(term, Split, Opts{Side: SideRight})
	r.Move(probs, Floating, Opts{Rect: render.XYWH(1, 2, 3, 4)})
	r.ToggleZoom(term)
	got, err := Restore(r.Save())
	if err != nil {
		t.Fatal(err)
	}
	if got.Dump() != r.Dump() {
		t.Errorf("restored:\n%s\nsaved:\n%s", got.Dump(), r.Dump())
	}
	if _, err := Restore(Snapshot("{")); err == nil {
		t.Error("restored nonsense")
	}
}

func TestMoveErrors(t *testing.T) {
	r := start(t)
	if err := r.Move(reqs, Tab, Opts{}); err == nil {
		t.Error("moved a view that isn't open")
	}
	if err := r.Open(reqs, Docked, Opts{Edge: "top"}); err == nil {
		t.Error("docked on a missing edge")
	}
}

// All lists every view wherever it is; Rename moves one's place, focus and zoom to another reference.
func TestAllAndRename(t *testing.T) {
	r := start(t)
	r.Move(probs, Floating, Opts{Rect: render.XYWH(1, 2, 3, 4)})
	r.ToggleZoom(code)
	focus := code
	r.Focus = &focus
	if got := fmt.Sprint(r.All()); got != fmt.Sprint([]ViewRef{code, test, tree, term, probs}) {
		t.Errorf("all = %s", got)
	}
	revived := ViewRef{ID: "editor", Instance: "7"}
	before := strings.ReplaceAll(r.Dump(), code.String(), revived.String())
	r.Rename(code, revived)
	if r.Dump() != before || *r.Focus != revived || *r.Zoomed != revived {
		t.Errorf("after the rename:\n%s\nfocus %v zoomed %v", r.Dump(), r.Focus, r.Zoomed)
	}
}

// OnShow hears of each view put in front, opened or shown, and keeps listening across a Load: what sits over the
// layout (a page) can give way to it.
func TestOnShow(t *testing.T) {
	r := New()
	var got []string
	r.OnShow(func(v ViewRef) { got = append(got, v.ID) })
	a, b := ViewRef{ID: "a"}, ViewRef{ID: "b"}
	if err := r.Open(a, Tab, Opts{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Open(b, Docked, Opts{Edge: Left}); err != nil {
		t.Fatal(err)
	}
	r.Show(a)
	if err := r.Load(r.Save()); err != nil {
		t.Fatal(err)
	}
	r.Show(b)
	if strings.Join(got, " ") != "a b a b" {
		t.Errorf("shown = %v, want a b a b", got)
	}
}

// A view moved or dropped anywhere stays open: OnClose hears only of a view closed.
func TestMovesDoNotClose(t *testing.T) {
	r := start(t)
	var closed []string
	r.OnClose(func(v ViewRef) { closed = append(closed, v.String()) })
	f := render.XYWH(1, 2, 300, 200)
	for _, err := range []error{
		r.Drop(code, Target{Dock: Bottom}),
		r.Drop(code, Target{Dock: Right}),
		r.Drop(code, Target{Float: &f}),
		r.Drop(code, Target{Beside: &test, Zone: ZoneRight}),
		r.Drop(code, Target{Beside: &test, Zone: ZoneCenter}),
		r.Drop(code, Target{Beside: &code, Zone: ZoneBottom}),
		r.Move(code, DropDown, Opts{}),
		r.Move(code, Tab, Opts{}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(closed) > 0 {
		t.Errorf("moves closed %v", closed)
	}
	r.Close(code)
	if strings.Join(closed, " ") != code.String() {
		t.Errorf("closed = %v, want %s", closed, code)
	}
}

// A drop where the view already is leaves the tree as it was, and shows the view.
func TestDropInPlace(t *testing.T) {
	split := func(r *Root) { r.Move(test, Split, Opts{Side: SideRight}) }
	for _, tc := range []struct {
		name  string
		setup func(r *Root)
		v     ViewRef
		t     Target
	}{
		{"a lone tab on its own leaf's middle", split, test, Target{Beside: &test, Zone: ZoneCenter}},
		{"a tab on its own leaf's middle, by another tab there", nil, test, Target{Beside: &code, Zone: ZoneCenter}},
		{"a docked view on its own dock", nil, probs, Target{Dock: Bottom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := start(t)
			if tc.setup != nil {
				tc.setup(r)
			}
			// The tree, without which tabs are in front or focused.
			tree := func() string {
				d := strings.ReplaceAll(r.Dump(), "*", "")
				return d[:strings.Index(d, "focus ")]
			}
			before := tree()
			if err := r.Drop(tc.v, tc.t); err != nil {
				t.Fatal(err)
			}
			if got := tree(); got != before {
				t.Errorf("the drop moved it:\n%s\nwas:\n%s", got, before)
			}
			if w := r.Find(tc.v); w.Leaf.Tabs[w.Leaf.Active] != tc.v || *r.Focus != tc.v {
				t.Errorf("%s isn't in front:\n%s", tc.v, r.Dump())
			}
		})
	}
}

// A drop with an Index puts the view in that slot of the leaf it joins, before the tab there: in its own leaf, a
// reorder.
func TestDropAtIndex(t *testing.T) {
	at := func(i int) *int { return &i }
	for _, tc := range []struct {
		name string
		v    ViewRef
		t    Target
		leaf func(r *Root) *Node
		want string
	}{
		{"to the front of its own leaf", test, Target{Beside: &code, Zone: ZoneCenter, Index: at(0)}, mainOf, "editor#round_test.go editor#round.go"},
		{"to the end of its own leaf", code, Target{Beside: &code, Zone: ZoneCenter, Index: at(2)}, mainOf, "editor#round_test.go editor#round.go"},
		{"to its own slot", code, Target{Beside: &test, Zone: ZoneCenter, Index: at(1)}, mainOf, "editor#round.go editor#round_test.go"},
		{"between another leaf's tabs", term, Target{Beside: &code, Zone: ZoneCenter, Index: at(1)}, mainOf,
			"editor#round.go terminal#1 editor#round_test.go"},
		{"to the front of its own dock", term, Target{Dock: Bottom, Index: at(0)}, bottomOf, "terminal#1 problems"},
		{"to the front of another dock", code, Target{Dock: Bottom, Index: at(0)}, bottomOf, "editor#round.go problems terminal#1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := start(t)
			if err := r.Drop(tc.v, tc.t); err != nil {
				t.Fatal(err)
			}
			l := tc.leaf(r)
			var got []string
			for _, v := range l.Tabs {
				got = append(got, v.String())
			}
			if strings.Join(got, " ") != tc.want {
				t.Errorf("tabs %v, want %s", got, tc.want)
			}
			if l.Tabs[l.Active] != tc.v || *r.Focus != tc.v {
				t.Errorf("%s isn't in front:\n%s", tc.v, r.Dump())
			}
		})
	}
}

func mainOf(r *Root) *Node   { return r.Main }
func bottomOf(r *Root) *Node { return r.Docks[Bottom].Leaf }
