package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/typeset"
)

var update = flag.Bool("update", false, "rewrite golden files")

// The headless shaper is fixed-pitch: at size 10 a character is 6 wide and a line 14 tall.
var body = TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: render.Color{R: 200, G: 200, B: 200, A: 255}}

// newUI is a UI with reduced motion: behaviour tests are of the settled UI, and motion has its own (motion_test.go).
func newUI() *UI {
	u := New(headless.Shaper{}, body)
	u.opts.ReducedMotion = true
	return u
}

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

// A scrolling box's grow children take what its view has left past the content, so a spacer pushes what follows to
// the foot; with more than fits, each keeps its content and the box scrolls.
func TestScrollingBoxGrowsItsChildren(t *testing.T) {
	for _, tc := range []struct {
		rows         int
		gap, footTop float32
	}{{2, 40, 90}, {5, 0, 125}} {
		u := newUI()
		var gap, foot ID
		u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
			c.Column("pane", Style{W: Px(100), H: Px(100), ScrollY: true}, func(c *Ctx) {
				for i := range tc.rows {
					c.Box(i, Style{W: Flex(1), H: Px(25)}, nil)
				}
				gap = c.Box("gap", Style{H: Flex(1)}, nil)
				foot = c.Box("foot", Style{W: Flex(1), H: Px(10)}, nil)
			})
		})
		g, _ := u.Rect(gap)
		f, _ := u.Rect(foot)
		if g.Dy() != tc.gap || f.Min.Y != tc.footTop {
			t.Errorf("%d rows: gap %v high, foot at %v; want %v and %v", tc.rows, g.Dy(), f.Min.Y, tc.gap, tc.footTop)
		}
	}
}

// A box's Scale draws its subtree that much bigger, sizes, padding, gaps, radii and text
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

// A row that Wraps puts what doesn't fit its width on the next line, each child its own width, lines Gap apart, and
// grows as tall as its lines: a strip of chips any number of plugins add to.
func TestWrappingRow(t *testing.T) {
	u := newUI()
	var row ID
	var chips []ID
	u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
		c.Column("page", Style{W: Px(250)}, func(c *Ctx) {
			row = c.Row("chips", Style{W: Flex(1), Gap: 10, Wrap: true, Pad: Pad(5)}, func(c *Ctx) {
				for i, w := range []float32{100, 80, 60, 230, 300} {
					chips = append(chips, c.Box(i, Style{W: Px(w), H: Px(20)}, nil))
				}
			})
		})
	})
	r := func(id ID) render.Rect { x, _ := u.Rect(id); return x }
	want := []render.Rect{
		render.XYWH(5, 5, 100, 20), render.XYWH(115, 5, 80, 20), // 60 more would pass the 240 inside
		render.XYWH(5, 35, 60, 20),
		render.XYWH(5, 65, 230, 20),
		render.XYWH(5, 95, 240, 20), // wider than the row: as wide as it, on a line of its own
	}
	for i, id := range chips {
		if got := r(id); got != want[i] {
			t.Errorf("chip %d is %v, want %v", i, got, want[i])
		}
	}
	if got := r(row); got != render.XYWH(0, 0, 250, 120) {
		t.Errorf("the row is %v, want 250×120: four lines and its padding", got)
	}
}

// A FlipX float that would run past the window's right edge opens on its anchor's left instead: a hover card beside a
// control in the window's top right corner.
func TestFloatFlipsAcross(t *testing.T) {
	for _, c := range []struct {
		left, want float32
	}{
		{10, 10 + 40 + 4},    // room on the right: beside it
		{340, 340 - 4 - 100}, // none: on its left
	} {
		u := newUI()
		var card ID
		build := func(cx *Ctx) {
			cx.Row("page", Style{W: Flex(1), H: Flex(1), Pad: Insets{L: c.left}}, func(cx *Ctx) {
				anchor := cx.Box("bars", Style{W: Px(40), H: Px(20)}, nil)
				card = cx.Box("card", Style{W: Px(100), H: Px(60), Z: LayerPopup, Float: &Float{To: anchor, At: render.Point{X: 1}, Offset: render.Point{X: 4}, FlipX: true}}, nil)
			})
		}
		u.Layout(render.Point{X: 400, Y: 300}, build)
		u.Layout(render.Point{X: 400, Y: 300}, build)
		if r, _ := u.Rect(card); r.Min.X != c.want {
			t.Errorf("anchor at %v: the card's left is %v, want %v", c.left, r.Min.X, c.want)
		}
	}
}

// A Flip float that would run past the window's bottom opens above its anchor instead: a menu under a control at the
// foot of the window.
func TestFloatFlips(t *testing.T) {
	for _, c := range []struct {
		top, want float32
	}{
		{10, 10 + 20 + 4},   // room below: under it
		{270, 270 - 4 - 60}, // none: above it
	} {
		u := newUI()
		var menu ID
		build := func(cx *Ctx) {
			cx.Column("page", Style{W: Flex(1), H: Flex(1), Pad: Insets{T: c.top}}, func(cx *Ctx) {
				anchor := cx.Box("btn", Style{W: Px(40), H: Px(20)}, nil)
				menu = cx.Box("menu", Style{W: Px(80), H: Px(60), Z: LayerPopup, Float: &Float{To: anchor, At: render.Point{Y: 1}, Offset: render.Point{Y: 4}, Flip: true}}, nil)
			})
		}
		u.Layout(render.Point{X: 400, Y: 300}, build)
		u.Layout(render.Point{X: 400, Y: 300}, build)
		if r, _ := u.Rect(menu); r.Min.Y != c.want {
			t.Errorf("anchor at %v: the menu's top is %v, want %v", c.top, r.Min.Y, c.want)
		}
	}
}

// A grow box that clips gives way to its siblings' share, as overflow: hidden does in CSS: its content is cut, not
// its neighbour. One that doesn't clip keeps room for its content.
func TestClippingGrowBoxTakesItsShare(t *testing.T) {
	u := newUI()
	long := strings.Repeat("x", 50) // 300 wide
	u.Layout(render.Point{X: 400, Y: 100}, func(c *Ctx) {
		c.Row("clips", Style{W: Flex(1), H: Px(20)}, func(c *Ctx) {
			c.Box("empty", Style{W: Flex(1), H: Flex(1)}, nil)
			c.Row("text", Style{W: Flex(1), Clip: true}, func(c *Ctx) { c.Text("t", long, body) })
		})
		c.Row("keeps", Style{W: Flex(1), H: Px(20)}, func(c *Ctx) {
			c.Box("empty", Style{W: Flex(1), H: Flex(1)}, nil)
			c.Row("text", Style{W: Flex(1)}, func(c *Ctx) { c.Text("t", long, body) })
		})
	})
	width := func(path ...any) float32 {
		r, _ := u.Rect(childID(childID(0, path[0]), path[1]))
		return r.Max.X - r.Min.X
	}
	if w := width("clips", "empty"); w != 200 {
		t.Errorf("beside a clipping box, the empty one is %v wide, want half: 200", w)
	}
	if w := width("keeps", "text"); w != 300 {
		t.Errorf("a box that doesn't clip is %v wide, want its content's 300", w)
	}
}
