package ui

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/typeset"
)

var update = flag.Bool("update", false, "rewrite golden files")

// The headless shaper is fixed-pitch: at size 10 a character is 6 wide and a line 14 tall.
var body = TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: render.Color{R: 200, G: 200, B: 200, A: 255}}

func newUI() *UI { return New(headless.Shaper{}, body) }

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "layout", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s:\n%s\nwant:\n%s", name, got, want)
	}
}

var cases = map[string]func(c *Ctx){
	// Fixed, fit, then the rest shared 1:2 between two grow boxes, with padding and gaps.
	"row-sizing": func(c *Ctx) {
		c.Row("row", Style{W: Flex(1), H: Px(40), Pad: Pad(10), Gap: 5}, func(c *Ctx) {
			c.Box("fixed", Style{W: Px(50), H: Flex(1)}, nil)
			c.Text("label", "abc", TextStyle{})
			c.Box("one", Style{W: Flex(1), H: Px(10)}, nil)
			c.Box("two", Style{W: Flex(2), H: Px(10)}, nil)
		})
	},
	// Percent and grow heights in a column, centred on the main axis and pushed to the end across it.
	"column-align": func(c *Ctx) {
		c.Column("col", Style{W: Px(200), H: Px(300), Main: Center, Cross: End}, func(c *Ctx) {
			c.Box("a", Style{W: Px(100), H: Px(20)}, nil)
			c.Box("half", Style{W: Px(50), H: Pct(.5)}, nil)
			c.Box("b", Style{W: Px(50), H: Px(20)}, nil)
		})
		c.Column("between", Style{W: Px(200), H: Px(100), Main: SpaceBetween, Cross: Center}, func(c *Ctx) {
			c.Box("a", Style{W: Px(20), H: Px(10)}, nil)
			c.Box("b", Style{W: Px(40), H: Px(10)}, nil)
			c.Box("c", Style{W: Px(60), H: Px(10)}, nil)
		})
	},
	// A capped grow box gives what it can't take to the others; a floor holds when space runs out.
	"min-max": func(c *Ctx) {
		c.Row("capped", Style{W: Px(300), H: Px(10)}, func(c *Ctx) {
			c.Box("max60", Style{W: Flex(1).Clamp(0, 60)}, nil)
			c.Box("rest", Style{W: Flex(1)}, nil)
			c.Box("rest2", Style{W: Flex(1)}, nil)
		})
		c.Row("floored", Style{W: Px(100), H: Px(10)}, func(c *Ctx) {
			c.Box("fixed", Style{W: Px(90)}, nil)
			c.Box("min30", Style{W: Flex(1).Clamp(30, 0)}, nil)
		})
	},
	// Fit boxes wrap their content, through any depth.
	"nested-fit": func(c *Ctx) {
		c.Row("chip", Style{Pad: PadXY(7, 2), Gap: 4, Cross: Center, Fill: render.Color{A: 255}, Radius: 10}, func(c *Ctx) {
			c.Box("dot", Style{W: Px(7), H: Px(7)}, nil)
			c.Column("lines", Style{Gap: 2}, func(c *Ctx) {
				c.Text(nil, "claude", TextStyle{})
				c.Text(nil, "working", TextStyle{})
			})
		})
		c.Node(RowNode(TextNode("from"), TextNode("a view")))
	},
	// Scrolled content moves, clamped to what there is to scroll.
	"scroll": func(c *Ctx) {
		c.Column("pane", Style{W: Px(100), H: Px(100), ScrollY: true}, func(c *Ctx) {
			for i := range 10 {
				c.Box(i, Style{W: Flex(1), H: Px(30)}, nil)
			}
		})
	},
	// Floats sit against another node, a point, or their parent, outside the flow.
	"floats": func(c *Ctx) {
		var target ID
		c.Row("line", Style{W: Flex(1), H: Px(20), Pad: Pad(5)}, func(c *Ctx) {
			target = c.Text("word", "roundHalfEven", TextStyle{})
			c.Box("card", Style{W: Px(80), H: Px(30), Z: 2, Float: &Float{To: target, At: render.Point{X: 0, Y: 1}, Offset: render.Point{Y: 4}}}, nil)
			c.Box("tip", Style{W: Px(40), H: Px(10), Z: 1, Float: &Float{Point: &render.Point{X: 300, Y: 200}, Self: render.Point{X: .5, Y: 1}}}, nil)
			c.Box("corner", Style{W: Px(10), H: Px(10), Float: &Float{At: render.Point{X: 1}, Self: render.Point{X: 1}}}, nil)
		})
	},
	// Duplicate keys among siblings still get distinct ids.
	"duplicate-keys": func(c *Ctx) {
		c.Row("r", Style{}, func(c *Ctx) {
			c.Text("x", "a", TextStyle{})
			c.Text("x", "b", TextStyle{})
			c.Text("x", "c", TextStyle{})
		})
	},
}

func TestLayoutGolden(t *testing.T) {
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			u := newUI()
			u.Layout(render.Point{X: 400, Y: 300}, build)
			if name == "scroll" {
				id := childID(0, "pane")
				u.ScrollBy(id, 0, 50)
				u.Layout(render.Point{X: 400, Y: 300}, build)
				first := u.Dump()
				u.ScrollBy(id, 0, 1000)
				u.Layout(render.Point{X: 400, Y: 300}, build)
				golden(t, name, first+"--- scrolled past the end\n"+u.Dump())
				return
			}
			golden(t, name, u.Dump())
		})
	}
}

// A Fill float placed against its parent and sized Flex fills the parent along that axis, whatever the parent's
// content made it: a card's coloured edge.
func TestFloatFillsItsParent(t *testing.T) {
	u := newUI()
	var card, edge, other ID
	u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
		card = c.Row("card", Style{W: Px(200), Pad: Pad(9)}, func(c *Ctx) {
			edge = c.Box("edge", Style{W: Px(2), H: Flex(1), Float: &Float{Fill: true}}, nil)
			c.Box("body", Style{W: Px(100), H: Px(57)}, nil)
			other = c.Box("tip", Style{W: Px(2), H: Flex(1), Float: &Float{Fill: true, Point: &render.Point{X: 5, Y: 5}}}, nil)
		})
	})
	r := func(id ID) render.Rect { x, _ := u.Rect(id); return x }
	if got, want := r(edge), render.XYWH(0, 0, 2, 75); got != want {
		t.Errorf("the edge is %v, want %v: the card's height", got, want)
	}
	if r(card).Dy() != 75 {
		t.Errorf("the card is %v: the edge changed its size", r(card))
	}
	if r(other).Dy() != 0 {
		t.Errorf("a float placed at a point grew to %v", r(other))
	}
}

// theming.md, "Sizes": a box's Scale draws its subtree that much bigger, sizes, padding, gaps, radii and text
// alike, so a part with its own text size holds it; it multiplies a scaled parent's.
func TestScaledSubtree(t *testing.T) {
	u := newUI()
	var inner, text, nested ID
	u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
		c.Row("bar", Style{Scale: 1.5, Pad: Pad(4), Gap: 2}, func(c *Ctx) {
			inner = c.Box("icon", Style{W: Px(10), H: Px(10)}, nil)
			text = c.Text("t", "abc", TextStyle{})
			c.Box("more", Style{Scale: 2}, func(c *Ctx) {
				nested = c.Box("n", Style{W: Px(10), H: Px(10)}, nil)
			})
		})
	})
	r := func(id ID) render.Rect { x, _ := u.Rect(id); return x }
	if got := r(inner); got != render.XYWH(6, 6, 15, 15) {
		t.Errorf("a 10px box with 4px padding at 1.5× is %v, want 15px at 6,6", got)
	}
	if got := r(text); got.Min.X != 6+15+3 || got.Dx() != 3*6*1.5 {
		t.Errorf("the text after a 2px gap at 1.5× is %v, want at x 24 and 27 wide (three 9px characters)", got)
	}
	if got := r(nested); got.Dx() != 30 {
		t.Errorf("a 10px box at 2× inside 1.5× is %v wide, want 30", got.Dx())
	}
}
