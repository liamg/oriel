package widget

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	cyan   = render.Color{R: 0x4c, G: 0xc9, B: 0xf0, A: 255}
	pink   = render.Color{R: 0xff, G: 0x6e, B: 0xb4, A: 255}
	orange = render.Color{R: 0xff, G: 0x9f, B: 0x43, A: 255}
)

func money(v float64) string { return fmt.Sprintf("$%.2f", v) }

// chartFixture is a page of each kind of chart: stacked bars with an axis, a legend, a band and a mark; lines with a
// dashed projection over bars; stacked areas; a sparkline; and a strip.
type chartFixture struct {
	res     ChartResult
	strip   ChartResult
	bars    ui.ID
	stripID ui.ID
	table   bool
}

func (f *chartFixture) build(c *ui.Ctx) {
	c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(20), Gap: 18, Fill: c.Theme().C(theme.BgBase)}, func(c *ui.Ctx) {
		f.bars = c.Next("bars")
		f.res = Chart(c, "bars", []ChartSeries{
			{Name: "pricing", Values: []float64{4, 6, 0, 8, 3}, Tone: cyan},
			{Name: "billing-ui", Values: []float64{2, 1, 0, 6, 2}, Tone: pink},
		}, ChartOpts{Height: 140, Labels: []string{"Mon", "Tue", "Wed", "Thu", "Fri"}, LabelEvery: 1, YTicks: 2, YFormat: money,
			Format: money, Legend: true, Hover: true, Highlight: -1, Bands: []ChartBand{{From: 2, To: 3, Label: "idle"}},
			Marks: []ChartMark{{At: 3, Label: "compacted", Tone: orange, Dashed: true}}, Ticks: []float64{0, 0.5, 1, 3, 4}, TicksLabel: "turns",
			Table: f.table, Label: "cost per repo per day", Help: "test/chart"})
		Chart(c, "lines", []ChartSeries{
			{Name: "5-hour", Values: []float64{20, 40, 0, 30, 35}, Tone: render.Color{R: 0xe0, G: 0xb0, B: 0x30, A: 120}, Kind: ChartBars},
			{Name: "weekly", Values: []float64{40, 50, 55, 70, math.NaN()}, Tone: orange},
			{Name: "projection", Values: []float64{math.NaN(), math.NaN(), math.NaN(), 70, 100}, Tone: orange, Dashed: true, From: 3},
		}, ChartOpts{Kind: ChartLine, Height: 100, Max: 100, YTicks: 2, YFormat: func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
			Highlight: -1, Marks: []ChartMark{{At: 3, Label: "now"}}, Label: "window usage", Help: "test/chart"})
		Chart(c, "areas", []ChartSeries{
			{Name: "input", Values: []float64{1, 2, 3, 2}, Tone: cyan},
			{Name: "output", Values: []float64{2, 2, 1, 3}, Tone: orange},
		}, ChartOpts{Kind: ChartArea, Height: 60, Highlight: 1, Label: "areas", Help: "test/chart"})
		c.Row("small", ui.Style{Gap: 20, W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			Chart(c, "spark", []ChartSeries{{Name: "30 days", Values: []float64{1, 3, 2, 5, 4, 6, 2}, Tone: pink}},
				ChartOpts{Kind: ChartSpark, Width: ui.Px(120), Highlight: -1, Label: "spark", Help: "test/chart"})
			f.stripID = c.Next("strip")
			f.strip = Chart(c, "strip", []ChartSeries{{Name: "pricing", Values: []float64{68}, Tone: cyan},
				{Name: "billing-ui", Values: []float64{32}, Tone: pink}}, ChartOpts{Kind: ChartStrip, Highlight: -1, Label: "split", Help: "test/chart"})
		})
	})
}

func TestChartSnapshot(t *testing.T) {
	f := &chartFixture{}
	size := render.Point{X: 640, Y: 520}
	h := newHarness(t, theme.Dark, size, f.build)
	h.frame()
	snapshot.Golden(t, "testdata/chart.list", []byte(snapshot.List(h.list)))
	snapshot.GoldenPNG(t, "testdata/chart.png", raster.Render(h.list, size, 1, resolved(t, theme.Dark).C(theme.BgBase)), 0.02, 0.001)
}

// The pointer over a stacked bar picks out its point and series, and the hover card shows the point's values; a
// click on it says so.
func TestChartHoverAndClick(t *testing.T) {
	f := &chartFixture{}
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 520}, f.build)
	h.frame()
	plot := h.rect(ui.Child(f.bars, "plot"))
	// Thursday (the 4th of 5 points) has pricing 8 under billing-ui 6 on a $20 axis: pricing fills the bottom 40%.
	axisW := float32(chartAxisW)
	x := plot.Min.X + axisW + (plot.Dx()-axisW)*3.5/5
	y := plot.Min.Y + 140*0.8
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.frame()
	if f.res.Point != 3 || f.res.Series != 0 {
		t.Fatalf("hover = %+v, want point 3 on pricing", f.res)
	}
	if !strings.Contains(frameText(h), "Thu · $14.00") {
		t.Errorf("the hover card lacks the point's total: %q", frameText(h))
	}
	h.pointer(render.PointerMove, 0, x, plot.Min.Y+140*0.5)
	h.frame()
	h.frame()
	if f.res.Series != 1 {
		t.Errorf("over billing-ui's segment: %+v", f.res)
	}
}

func TestChartClickReportsTheSeries(t *testing.T) {
	f := &chartFixture{}
	var clicked ChartResult
	build := func(c *ui.Ctx) {
		f.build(c)
		if f.res.Clicked {
			clicked = f.res
		}
	}
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 520}, build)
	h.frame()
	plot := h.rect(ui.Child(f.bars, "plot"))
	x := plot.Min.X + chartAxisW + (plot.Dx()-chartAxisW)*1.5/5
	h.click(x, plot.Min.Y+140*0.9)
	if !clicked.Clicked || clicked.Point != 1 || clicked.Series != 0 {
		t.Errorf("click = %+v, want pricing on Tuesday", clicked)
	}
}

// The strip's segments take their shares of its width, and the pointer on one picks it out.
func TestChartStrip(t *testing.T) {
	f := &chartFixture{}
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 520}, f.build)
	h.frame()
	strip := ui.Child(ui.Child(f.stripID, "strip"), 0)
	a, b := h.rect(strip), h.rect(ui.Child(ui.Child(f.stripID, "strip"), 1))
	if r := a.Dx() / (a.Dx() + b.Dx()); math.Abs(float64(r)-.68) > .01 {
		t.Errorf("pricing takes %.2f of the strip, want .68", r)
	}
	x, y := mid(b)
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.frame()
	if f.strip.Series != 1 {
		t.Errorf("strip hover = %+v, want billing-ui", f.strip)
	}
}

// The table view lists each point's values: the chart without the picture.
func TestChartTableView(t *testing.T) {
	f := &chartFixture{table: true}
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 700}, f.build)
	h.frame()
	text := frameText(h)
	for _, want := range []string{"PRICING", "BILLING-UI", "Thu", "$8.00", "$6.00"} {
		if !strings.Contains(text, want) {
			t.Errorf("the table lacks %q", want)
		}
	}
}

func TestNiceMax(t *testing.T) {
	for v, want := range map[float64]float64{0: 1, 7: 10, 14: 20, 21.4: 25, 412.8: 500, 0.3: .5, 100: 100} {
		if got := niceMax(v); math.Abs(got-want) > 1e-9 {
			t.Errorf("niceMax(%v) = %v, want %v", v, got, want)
		}
	}
}

func frameText(h *harness) string {
	var out []string
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs {
			out = append(out, op.Run.Text)
		}
	}
	return strings.Join(out, "\n")
}
