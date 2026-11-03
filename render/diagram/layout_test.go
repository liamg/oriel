package diagram

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

var testFonts = Fonts{
	Label: typeset.FontSpec{Family: typeset.UI, Size: 13},
	Small: typeset.FontSpec{Family: typeset.UI, Size: 11.5},
	Title: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.Medium, Tracking: .12},
}

func shaper(t testing.TB) typeset.Shaper {
	t.Helper()
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func layout(t *testing.T, sh typeset.Shaper, src string) *Layout {
	t.Helper()
	d, err := Parse(src)
	if err != nil {
		t.Fatalf("%q: %v", src, err)
	}
	return d.Layout(sh, testFonts)
}

// flowcharts the layout invariants are checked on: the spec's examples and the design docs' shapes.
var flowcharts = map[string]string{
	"plan":   "flowchart LR\n  usage.Meter --> pricing.Round --> billing.Invoice",
	"decide": "flowchart TD\n    A[Start] --> B{Is it?}\n    B -->|Yes| C[OK]\n    C --> D[Rethink]\n    D --> B\n    B ---->|No| E[End]",
	"subgraphs": "flowchart TB\n    c1-->a2\n    subgraph one\n    a1-->a2\n    end\n    subgraph two\n    b1-->b2\n    end\n    subgraph three\n    c1-->c2\n    end\n" +
		"    one --> two\n    three --> two\n    two --> c2",
	"nested": "flowchart LR\n  subgraph TOP\n    direction TB\n    subgraph B1\n        direction RL\n        i1 -->f1\n    end\n    subgraph B2\n        direction BT\n        i2 -->f2\n    end\n  end\n  A --> TOP --> B\n  B1 --> B2",
	"architecture": "flowchart LR\n  subgraph oriel_one_process_per_machine\n    UI[\"ui + render\"]\n    Core[\"core store\"]\n    Sess[\"session host (host)\"]\n" +
		"    Agent[\"agent provider (claude / codex)\"]\n    LSP[\"lsp + dap\"]\n    PTY[\"pty / terminals\"]\n    UI --> Core\n    UI -- \"Go calls\" --> Sess\n    Sess --> Core\n  end\n" +
		"  subgraph sandbox_per_workspace\n    Tools[\"agent tools, builds, tests\"]\n    GW[\"gateway MCP\"]\n    Egress[\"egress proxy\"]\n  end\n" +
		"  Sess --> Agent\n  Agent --> Tools\n  Tools --> GW\n  Tools --> Egress\n  Sess --> LSP\n  Sess --> PTY",
	"layers": "flowchart TB\n  App[\"app: screens, panels, HUD\"] --> UI[\"ui: widgets, layout, theme, motion\"]\n  UI --> R[\"render: Surface, Path, Text, Image, Effect\"]\n" +
		"  R --> Gio[\"render/gio\"]\n  R --> Wgpu[\"render/wgpu (later)\"]\n  UI --> Text[\"typeset: shaping, fallback, atlas\"]\n  Text --> R",
	"shapes": "flowchart LR\n  a[rect] --> b(round) --> c([stadium]) --> d[[subroutine]]\n  d --> e[(database)] --> f((circle)) --> g>flag]\n" +
		"  g --> h{diamond} --> i{{hexagon}} --> j[/lean/] --> k[\\trapezoid/] --> l(((double)))",
	"bt":     "flowchart BT\n  a --> b & c\n  b --> d\n  c -.-> d\n  d ==> e",
	"rl":     "flowchart RL\n  a -->|one| b --> c\n  a --> c\n  c --> a",
	"loops":  "flowchart TD\n  a --> a\n  a -->|again| b\n  b -->|retry| b",
	"fanout": "flowchart TD\n  hub --> a & b & c & d & e\n  a & b & c --> sink\n  e --- f\n  d ~~~ f",
	"wide":   "graph TD\n  A[\"A very long label that has to wrap over more than one line in its box\"] --> B\n  B -- \"a long edge label that wraps too\" --> C",
}

func overlap(a, b render.Rect, gap float32) bool {
	return a.Min.X < b.Max.X+gap && b.Min.X < a.Max.X+gap && a.Min.Y < b.Max.Y+gap && b.Min.Y < a.Max.Y+gap
}

func contains(outer, inner render.Rect) bool {
	return inner.Min.X >= outer.Min.X-.01 && inner.Min.Y >= outer.Min.Y-.01 && inner.Max.X <= outer.Max.X+.01 && inner.Max.Y <= outer.Max.Y+.01
}

// onBorder says whether p is on the outline of a node shaped s in r.
func onBorder(s Shape, r render.Rect, p render.Point) bool {
	cx, cy, hw, hh := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, r.Dx()/2, r.Dy()/2
	dx, dy := float64((p.X-cx)/hw), float64((p.Y-cy)/hh)
	switch s {
	case Diamond:
		return math.Abs(math.Abs(dx)+math.Abs(dy)-1) < .02
	case Circle, DoubleCircle:
		return math.Abs(math.Hypot(dx, dy)-1) < .02
	}
	in := p.X >= r.Min.X-.5 && p.X <= r.Max.X+.5 && p.Y >= r.Min.Y-.5 && p.Y <= r.Max.Y+.5
	// the slanted and notched shapes meet edges inside their box, on the slant
	if s == Lean || s == LeanLeft || s == Trapezoid || s == TrapezoidAlt || s == Flag {
		return in
	}
	edge := abs(p.X-r.Min.X) < .5 || abs(p.X-r.Max.X) < .5 || abs(p.Y-r.Min.Y) < .5 || abs(p.Y-r.Max.Y) < .5
	return in && edge
}

func TestFlowchartLayoutInvariants(t *testing.T) {
	sh := shaper(t)
	for name, src := range flowcharts {
		t.Run(name, func(t *testing.T) {
			d, _ := Parse(src)
			f := d.Flow
			l := d.Layout(sh, testFonts)
			if len(l.Nodes) != len(f.Nodes) {
				t.Fatalf("%d boxes for %d nodes", len(l.Nodes), len(f.Nodes))
			}
			all := render.Rect{Max: l.Size}
			for i, a := range l.Nodes {
				if !contains(all, a.Rect) {
					t.Errorf("%s at %v is outside the diagram's %v", a.ID, a.Rect, l.Size)
				}
				for _, c := range l.Nodes[i+1:] {
					if overlap(a.Rect, c.Rect, 4) {
						t.Errorf("%s %v and %s %v overlap", a.ID, a.Rect, c.ID, c.Rect)
					}
				}
				for gi, g := range l.Groups {
					member := false
					for k := f.Nodes[i].Group; k >= 0; k = f.Groups[k].Parent {
						member = member || k == gi
					}
					switch {
					case member && !contains(g.Rect, a.Rect):
						t.Errorf("%s %v is outside its subgraph %s %v", a.ID, a.Rect, g.ID, g.Rect)
					case !member && overlap(g.Rect, a.Rect, 0):
						t.Errorf("%s %v is inside subgraph %s %v, not its own", a.ID, a.Rect, g.ID, g.Rect)
					}
				}
			}
			for i, g := range l.Groups {
				if p := f.Groups[i].Parent; p >= 0 && !contains(l.Groups[p].Rect, g.Rect) {
					t.Errorf("subgraph %s %v is outside its parent %s %v", g.ID, g.Rect, l.Groups[p].ID, l.Groups[p].Rect)
				}
				for j, h := range l.Groups[i+1:] {
					j += i + 1
					nested := false
					for k := f.Groups[j].Parent; k >= 0; k = f.Groups[k].Parent {
						nested = nested || k == i
					}
					for k := f.Groups[i].Parent; k >= 0; k = f.Groups[k].Parent {
						nested = nested || k == j
					}
					if !nested && overlap(g.Rect, h.Rect, 0) {
						t.Errorf("subgraphs %s %v and %s %v overlap", g.ID, g.Rect, h.ID, h.Rect)
					}
				}
			}
			for _, e := range l.Edges {
				fe := f.Edges[e.Edge]
				if len(e.Points) < 2 {
					t.Errorf("%s→%s has %d points", fe.From, fe.To, len(e.Points))
					continue
				}
				a, z := e.Points[0], e.Points[len(e.Points)-1]
				if !e.FromGroup && !onBorder(l.Nodes[e.From].Shape, l.Nodes[e.From].Rect, a) {
					t.Errorf("%s→%s starts at %v, off %s's border %v", fe.From, fe.To, a, fe.From, l.Nodes[e.From].Rect)
				}
				if !e.ToGroup && !onBorder(l.Nodes[e.To].Shape, l.Nodes[e.To].Rect, z) {
					t.Errorf("%s→%s ends at %v, off %s's border %v", fe.From, fe.To, z, fe.To, l.Nodes[e.To].Rect)
				}
				if e.ToGroup && !onRectBorder(l.Groups[e.To].Rect, z) || e.FromGroup && !onRectBorder(l.Groups[e.From].Rect, a) {
					t.Errorf("%s→%s doesn't meet its subgraph's box: %v", fe.From, fe.To, e.Points)
				}
				if e.Label.Dx() > 0 {
					for _, n := range l.Nodes {
						if overlap(e.Label, n.Rect, 0) {
							t.Errorf("%s→%s's label %v covers %s %v", fe.From, fe.To, e.Label, n.ID, n.Rect)
						}
					}
				}
				if e.Back || e.Loop || e.FromGroup || e.ToGroup {
					continue
				}
				from, to := l.Nodes[e.From].Rect, l.Nodes[e.To].Rect
				var ok bool
				switch f.Dir {
				case TB:
					ok = to.Min.Y >= from.Max.Y
				case BT:
					ok = to.Max.Y <= from.Min.Y
				case LR:
					ok = to.Min.X >= from.Max.X
				case RL:
					ok = to.Max.X <= from.Min.X
				}
				if !ok {
					t.Errorf("%s %v → %s %v runs against the direction", fe.From, from, fe.To, to)
				}
			}
		})
	}
}

func onRectBorder(r render.Rect, p render.Point) bool {
	return onBorder(Rect, r, p)
}

// A cycle is broken by drawing one of its edges back: every other edge still runs with the direction.
func TestFlowchartCycles(t *testing.T) {
	l := layout(t, shaper(t), flowcharts["decide"])
	back := 0
	for _, e := range l.Edges {
		if e.Back {
			back++
		}
	}
	if back != 1 {
		t.Errorf("%d edges drawn back in a one-cycle chart", back)
	}
}

// ref:   mermaid/flowchart.md#Minimum length of a link
// quote: "so that it spans two more ranks than regular links"
func TestFlowchartLinkLength(t *testing.T) {
	l := layout(t, shaper(t), "flowchart TD\n  a --> b --> c --> d\n  a ---> x\n  a ----> y")
	mid := func(i int) float32 { return (l.Nodes[i].Rect.Min.Y + l.Nodes[i].Rect.Max.Y) / 2 }
	if !(mid(4) > mid(1)+10 && math.Abs(float64(mid(4)-mid(2))) < 1 && math.Abs(float64(mid(5)-mid(3))) < 1) {
		t.Errorf("ranks: b %v c %v d %v x %v y %v", mid(1), mid(2), mid(3), mid(4), mid(5))
	}
}

// Edges that can cross needn't: a chart drawn as a ladder has none.
func TestFlowchartCrossings(t *testing.T) {
	l := layout(t, shaper(t), "flowchart TD\n  a1 --> b2\n  a2 --> b1\n  a1 --> c1\n  b1 --> c2\n  b2 --> c1")
	x := func(id string) float32 {
		for _, n := range l.Nodes {
			if n.ID == id {
				return n.Rect.Min.X
			}
		}
		return -1
	}
	if (x("a1") < x("a2")) != (x("b2") < x("b1")) {
		t.Errorf("a1→b2 and a2→b1 cross: a1 %v a2 %v b1 %v b2 %v", x("a1"), x("a2"), x("b1"), x("b2"))
	}
}

func TestSequenceLayout(t *testing.T) {
	src := "sequenceDiagram\n    autonumber\n    actor U as User\n    participant A as Alice\n    U->>A: Open the plan\n    A->>+John: Hello John, how are you?\n" +
		"    loop Every minute\n        John-->>A: Great!\n        John->>John: Fight against hypochondria\n    end\n    Note right of John: Rational thoughts!\n" +
		"    alt is sick\n        John-xA: Not so good :(\n    else is well\n        John--)A: Feeling fresh like a daisy\n    end\n    Note over A,John: A typical interaction\n    John-->>-A: Bye"
	l := layout(t, shaper(t), src)
	if len(l.Nodes) != 3 {
		t.Fatalf("participants %+v", l.Nodes)
	}
	for i := 1; i < len(l.Nodes); i++ {
		if l.Nodes[i].Rect.Min.X <= l.Nodes[i-1].Rect.Max.X {
			t.Errorf("%s %v isn't right of %s %v", l.Nodes[i].ID, l.Nodes[i].Rect, l.Nodes[i-1].ID, l.Nodes[i-1].Rect)
		}
	}
	var last float32
	for i, e := range l.Edges {
		y := e.Points[0].Y
		if y <= last || y <= l.Nodes[0].Rect.Max.Y {
			t.Errorf("message %d at y %v, after %v", i, y, last)
		}
		last = y
		if e.Loop {
			continue
		}
		cf := (l.Nodes[e.From].Rect.Min.X + l.Nodes[e.From].Rect.Max.X) / 2
		ct := (l.Nodes[e.To].Rect.Min.X + l.Nodes[e.To].Rect.Max.X) / 2
		if abs(e.Points[0].X-cf) > seqActW || abs(e.Points[1].X-ct) > seqActW {
			t.Errorf("message %d runs %v, between lifelines at %v and %v", i, e.Points, cf, ct)
		}
	}
	if len(l.Edges) != 7 {
		t.Errorf("%d messages", len(l.Edges))
	}
	if last >= l.Size.Y {
		t.Errorf("the last message at %v is past the diagram's height %v", last, l.Size.Y)
	}
}

// Laid out once, a diagram draws at any width, its strokes and text scaled with it.
func TestDrawScales(t *testing.T) {
	l := layout(t, shaper(t), flowcharts["plan"])
	p := ThemePalette(resolved(t, theme.Dark))
	sizes := map[float32]float32{}
	for _, w := range []float32{l.Size.X, l.Size.X / 2} {
		var list render.List
		l.Draw(list.Record(1), render.XYWH(10, 10, w, w*l.Size.Y/l.Size.X), p)
		for _, op := range list.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == "pricing.Round" {
				sizes[w] = op.Run.Font.Size
			}
			if op.Kind == render.OpRect && (op.Rect.Max.X > 10+w+.01 || op.Rect.Min.X < 10-.01) {
				t.Errorf("at %v wide, a box at %v is outside", w, op.Rect)
			}
		}
	}
	if sizes[l.Size.X] != 13 || sizes[l.Size.X/2] != 6.5 {
		t.Errorf("label sizes %v", sizes)
	}
}

func resolved(t testing.TB, v theme.Variant) *theme.Resolved {
	t.Helper()
	st, err := theme.Stack(theme.Builtins(), "oriel")
	if err != nil {
		t.Fatal(err)
	}
	r, err := theme.Resolve(st, v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// The look: each chart drawn at its own size on the presenter's surface, in both variants.
func TestDiagramSnapshots(t *testing.T) {
	sh := shaper(t)
	for _, name := range []string{"decide", "architecture", "shapes", "layers", "loops", "sequence"} {
		src := flowcharts[name]
		if name == "sequence" {
			src = "sequenceDiagram\n    autonumber\n    actor U as User\n    participant P as Plan doc\n    participant C as Claude\n    U->>P: Comment on L14\n" +
				"    P->>+C: Deliver the thread\n    loop Until it's resolved\n        C-->>P: Reply\n        C->>C: Read billing.md\n    end\n" +
				"    Note right of C: Half-even, as Stripe does\n    alt confirmed\n        U->>P: Resolve\n    else not yet\n        U--)C: Ask again\n    end\n    C-->>-U: Edited PLAN.md"
		}
		l := layout(t, sh, src)
		for _, v := range []theme.Variant{theme.Dark, theme.Light} {
			th := resolved(t, v)
			size := render.Point{X: float32(math.Ceil(float64(l.Size.X))) + 32, Y: float32(math.Ceil(float64(l.Size.Y))) + 32}
			var list render.List
			fr := list.Record(2)
			fr.Rect(render.Rect{Max: size}, render.Style{Fill: th.C(theme.BgSurface)})
			l.Draw(fr, render.XYWH(16, 16, l.Size.X, l.Size.Y), ThemePalette(th))
			file := fmt.Sprintf("testdata/%s-%s.png", name, v)
			snapshot.GoldenPNG(t, file, raster.Render(&list, size, 2, th.C(theme.BgSurface)), 0.02, 0.002)
		}
	}
}

// Every built-in variant keeps a diagram's text readable on what it's drawn on.
func TestThemeReadable(t *testing.T) {
	for _, v := range []theme.Variant{theme.Dark, theme.Light} {
		for _, f := range theme.Lint(resolved(t, v)) {
			if strings.Contains(f.Message, "diagram.") {
				t.Errorf("%s: %s", v, f.Message)
			}
		}
	}
}

// Labels wrap rather than cut: only a word too long for any box, a label past its lines, or a subgraph's title wider
// than its box is cut short, and the layout says where, with the whole text, so a tooltip can show it.
func TestTruncatedText(t *testing.T) {
	long := "pricing.RoundingModeHalfEvenWithBankersTiesAppliedPerInvoiceLineBeforeTheTotal"
	l := layout(t, shaper(t), "flowchart TB\n  a[\""+long+"\"] --> b[A label that is long enough to wrap onto a second line but no more]\n"+
		"  subgraph a_subgraph_whose_title_runs_far_past_its_one_small_node\n    c\n  end")
	var drawn []string
	for _, p := range l.prims {
		if p.kind == primText {
			drawn = append(drawn, p.text)
		}
	}
	if len(l.Truncated) != 2 {
		t.Fatalf("truncated: %+v; groups %+v; drawn %q", l.Truncated, l.Groups, drawn)
	}
	if tr := l.Truncated[1]; tr.Text != long || tr.Rect != l.Nodes[0].Rect {
		t.Errorf("the long word: %+v, its node at %v", tr, l.Nodes[0].Rect)
	}
	if tr := l.Truncated[0]; tr.Text != "a_subgraph_whose_title_runs_far_past_its_one_small_node" || !contains(l.Groups[0].Rect, tr.Rect) {
		t.Errorf("the title: %+v, its box %v", tr, l.Groups[0].Rect)
	}
	cut := 0
	for _, s := range drawn {
		if strings.HasSuffix(s, "…") {
			cut++
		}
		if strings.Contains(s, "wrap") && strings.Contains(s, "…") {
			t.Errorf("a label that wraps was cut: %q", s)
		}
	}
	if cut != 2 {
		t.Errorf("drawn: %q", drawn)
	}
	s := layout(t, shaper(t), "sequenceDiagram\n  participant "+long+"\n  "+long+"->>B: hi")
	if len(s.Truncated) != 1 || s.Truncated[0].Text != long || s.Truncated[0].Rect != s.Nodes[0].Rect {
		t.Errorf("a participant's name: %+v, at %v", s.Truncated, s.Nodes[0].Rect)
	}
}
