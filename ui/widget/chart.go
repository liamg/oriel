package widget

import (
	"fmt"
	"math"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tChartGrid  = theme.Declare("chart.grid", "line")
	tChartAxis  = theme.Declare("chart.axis", "fg.ghost")
	tChartBand  = theme.DeclareOn("chart.band", "alpha(fg, 3%)", "bg.surface")
	tChartHatch = theme.DeclareOn("chart.band.hatch", "alpha(fg, 7%)", "bg.surface")
	tChartMark  = theme.Declare("chart.mark", "fg.faint")
	tChartLabel = theme.Declare("chart.label", "fg.muted")
)

// ChartKind is how a chart, or one of its series, is drawn.
type ChartKind int

const (
	ChartInherit ChartKind = iota // a series drawn as its chart is
	ChartBars                     // stacked bars, one stack per point
	ChartArea                     // stacked areas
	ChartLine                     // lines over the points
	ChartSpark                    // a sparkline: one small line, no axes
	ChartStrip                    // a 100% bar: each series' first value as its share
)

// ChartSeries is one series of a chart: a value at each point (NaN for none), in its tone.
type ChartSeries struct {
	Name   string
	Values []float64
	Tone   render.Color
	Kind   ChartKind // ChartInherit draws it as the chart is
	Dashed bool      // a line drawn dashed: a projection
	From   int       // the first point a line is drawn from
}

// ChartMark is a vertical line at a point (fractional between points), with a label at its top: now, a reset.
type ChartMark struct {
	At     float64
	Label  string
	Tone   render.Color // chart.mark when zero
	Dashed bool
}

// ChartBand shades the points From to To (fractional), with a label: an idle stretch.
type ChartBand struct {
	From, To float64
	Label    string
}

type ChartOpts struct {
	Kind   ChartKind // ChartBars when ChartInherit
	Width  ui.Size   // fills by default
	Height float32   // the plot's; 120 if 0 (16 for a sparkline, 14 for a strip)
	Labels []string  // a label per point, under the plot
	// LabelEvery shows every nth label; 0 fits them.
	LabelEvery int
	Max        float64 // the value at the top; 0 takes it from the data, rounded up
	YFormat    func(float64) string
	YTicks     int                  // grid lines with values on the left; 0 for none
	Format     func(float64) string // values in the hover card and table
	Title      func(point int) string
	Legend     bool
	Hover      bool // a card of the point's values under the pointer
	Marks      []ChartMark
	Bands      []ChartBand
	Ticks      []float64 // small ticks in a strip under the plot: turns
	TicksLabel string
	Highlight  int  // the series lit, the others dimmed; -1 for none
	Table      bool // the data as a table instead: the chart's table view
	Label      string
	Help       string
}

// ChartResult is what the pointer is on.
type ChartResult struct {
	Series  int  // the series under the pointer, or in the legend; -1
	Point   int  // the point under the pointer; -1
	Clicked bool // the plot or a legend entry was clicked
}

const chartAxisW = 36

func (o ChartOpts) kind() ChartKind {
	if o.Kind == ChartInherit {
		return ChartBars
	}
	return o.Kind
}

func (s ChartSeries) kindIn(o ChartOpts) ChartKind {
	if s.Kind == ChartInherit {
		return o.kind()
	}
	return s.Kind
}

func points(series []ChartSeries) int {
	n := 0
	for _, s := range series {
		n = max(n, len(s.Values))
	}
	return n
}

func value(s ChartSeries, i int) float64 {
	if i < 0 || i >= len(s.Values) || math.IsNaN(s.Values[i]) {
		return 0
	}
	return s.Values[i]
}

// niceMax is v rounded up to 1, 2, 2.5 or 5 times a power of ten.
func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*p*1.0000001 {
			return m * p
		}
	}
	return 10 * p
}

// top is the chart's value at its top: the stacked bars' and areas' highest, and the lines'.
func (o ChartOpts) top(series []ChartSeries) float64 {
	if o.Max > 0 {
		return o.Max
	}
	n := points(series)
	hi := 0.0
	for i := range n {
		stack := 0.0
		for _, s := range series {
			switch s.kindIn(o) {
			case ChartBars, ChartArea:
				stack += value(s, i)
			default:
				hi = max(hi, value(s, i))
			}
		}
		hi = max(hi, stack)
	}
	if o.kind() == ChartSpark {
		return max(hi, 1e-9)
	}
	if o.YTicks > 0 {
		return niceMax(hi/float64(o.YTicks)) * float64(o.YTicks) // grid lines at round values
	}
	return niceMax(hi)
}

// Chart draws series as o says: stacked bars, areas, lines, a sparkline or a 100% strip, with an axis, a legend,
// marks and bands, and a card of the values under the pointer. With Table it's the same data as a table.
func Chart(c *ui.Ctx, key any, series []ChartSeries, o ChartOpts) ChartResult {
	res := ChartResult{Series: -1, Point: -1}
	w := o.Width
	if w == (ui.Size{}) {
		w = ui.Flex(1)
	}
	c.Column(key, ui.Style{W: w, Gap: 6, Role: ui.RoleGroup, Label: o.Label, Help: o.Help}, func(c *ui.Ctx) {
		if o.Legend {
			if s, clicked := chartLegend(c, series, o); s >= 0 {
				res.Series, res.Clicked = s, clicked
			}
		}
		if o.Table {
			chartTable(c, series, o)
			return
		}
		switch o.kind() {
		case ChartStrip:
			if s, clicked := chartStrip(c, series, o); s >= 0 {
				res.Series, res.Clicked = s, clicked
			}
		default:
			r := chartPlot(c, series, o)
			if r.Series >= 0 || r.Point >= 0 {
				res = r
			}
		}
	})
	return res
}

func dim(tone render.Color, on bool) render.Color {
	if on {
		return tone
	}
	return alpha(tone, .3)
}

func lit(o ChartOpts, i int) bool { return o.Highlight < 0 || o.Highlight == i }

func chartLegend(c *ui.Ctx, series []ChartSeries, o ChartOpts) (int, bool) {
	hover, clicked := -1, false
	c.Row("legend", ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		for i, s := range series {
			in := Clickable(c, i, ui.Style{Dir: ui.Row, Gap: 5, Cross: ui.Center, NoHover: true, Role: ui.RoleText, Label: s.Name, Help: o.Help},
				func(c *ui.Ctx) {
					c.Box("sw", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: dim(s.Tone, lit(o, i))}, nil)
					c.Text("n", s.Name, monoText(10.5, typeset.Normal, dim(col(c, tChartLabel), lit(o, i)), 0))
				})
			if in.Hovered || in.Inside {
				hover = i
			}
			if in.Clicked {
				hover, clicked = i, true
			}
		}
	})
	return hover, clicked
}

// chartStrip is a 100% bar: each series' share of the whole, in its tone.
func chartStrip(c *ui.Ctx, series []ChartSeries, o ChartOpts) (int, bool) {
	h := o.Height
	if h == 0 {
		h = 14
	}
	total := 0.0
	for _, s := range series {
		total += value(s, 0)
	}
	hover, clicked := -1, false
	c.Row("strip", ui.Style{W: ui.Flex(1), H: ui.Px(h), Gap: 2, Radius: 3, Clip: true}, func(c *ui.Ctx) {
		for i, s := range series {
			v := value(s, 0)
			if v <= 0 || total <= 0 {
				continue
			}
			in := Clickable(c, i, ui.Style{W: ui.Flex(float32(v / total)), H: ui.Flex(1), Fill: dim(s.Tone, lit(o, i)), NoHover: true,
				Role: ui.RoleText, Label: fmt.Sprintf("%s %d%%", s.Name, int(v/total*100+.5)), Tooltip: s.Name, Help: o.Help}, nil)
			if in.Hovered {
				hover = i
			}
			if in.Clicked {
				hover, clicked = i, true
			}
		}
	})
	return hover, clicked
}

// chartGeom is where a plot's points fall, from its rect.
type chartGeom struct {
	r     render.Rect
	n     int
	top   float64
	slot  float32
	lines bool // points at slot edges (lines and areas), not centres (bars)
}

func (g chartGeom) x(i float64) float32 {
	if g.lines {
		if g.n <= 1 {
			return g.r.Min.X
		}
		return g.r.Min.X + float32(i)*g.r.Dx()/float32(g.n-1)
	}
	return g.r.Min.X + (float32(i)+.5)*g.slot
}

func (g chartGeom) y(v float64) float32 {
	return g.r.Max.Y - float32(min(v, g.top)/g.top)*g.r.Dy()
}

// index is the point nearest x.
func (g chartGeom) index(x float32) int {
	if g.n == 0 {
		return -1
	}
	var i int
	if g.lines && g.n > 1 {
		i = int(math.Round(float64((x - g.r.Min.X) / (g.r.Dx() / float32(g.n-1)))))
	} else {
		i = int((x - g.r.Min.X) / g.slot)
	}
	return min(max(i, 0), g.n-1)
}

func chartPlot(c *ui.Ctx, series []ChartSeries, o ChartOpts) ChartResult {
	res := ChartResult{Series: -1, Point: -1}
	k := c.Scale()
	spark := o.kind() == ChartSpark
	h := o.Height
	if h == 0 {
		h = 120
		if spark {
			h = 16
		}
	}
	n := points(series)
	top := o.top(series)
	lines := true
	for _, s := range series {
		if s.kindIn(o) == ChartBars {
			lines = false
		}
	}
	axis := o.YTicks > 0 && o.YFormat != nil && !spark
	font := typeset.FontSpec{Family: typeset.Mono, Size: 9 * k}
	grid, axisFg, markFg := col(c, tChartGrid), col(c, tChartAxis), col(c, tChartMark)
	band, hatch := col(c, tChartBand), col(c, tChartHatch)
	plotKey := c.Next("plot")
	prev, _ := c.Rect(plotKey)
	g := func(r render.Rect) chartGeom {
		if axis {
			r.Min.X += chartAxisW * k
		}
		gm := chartGeom{r: r, n: n, top: top, lines: lines}
		if n > 0 {
			gm.slot = r.Dx() / float32(n)
		}
		return gm
	}
	// What the pointer is on, from the last frame's rect.
	in := Clickable(c, "plot", ui.Style{Dir: ui.Column, W: ui.Flex(1), NoHover: true, Cursor: render.CursorDefault, Role: ui.RoleText,
		Label: o.Label, Help: o.Help}, func(c *ui.Ctx) {
		var markRuns []*typeset.Run
		for _, m := range o.Marks {
			markRuns = append(markRuns, c.Shaper().Shape(m.Label, font))
		}
		var bandRuns []*typeset.Run
		for _, b := range o.Bands {
			bandRuns = append(bandRuns, c.Shaper().Shape(b.Label, font))
		}
		var tickRuns []*typeset.Run
		if axis {
			for t := 0; t <= o.YTicks; t++ {
				tickRuns = append(tickRuns, c.Shaper().Shape(o.YFormat(top*float64(t)/float64(o.YTicks)), font))
			}
		}
		hl := o.Highlight
		c.Paint("canvas", ui.Flex(1), ui.Px(h), func(f render.Frame, r render.Rect) {
			gm := g(r)
			for i, b := range o.Bands {
				x0, x1 := gm.x(b.From), gm.x(b.To)
				if !lines {
					x0, x1 = gm.r.Min.X+float32(b.From)*gm.slot, gm.r.Min.X+float32(b.To)*gm.slot
				}
				br := render.Rect{Min: render.Point{X: x0, Y: gm.r.Min.Y}, Max: render.Point{X: x1, Y: gm.r.Max.Y}}
				f.Rect(br, render.Style{Fill: band})
				f.PushClip(br, 0)
				var p render.Path
				for x := br.Min.X - br.Dy(); x < br.Max.X; x += 6 * k {
					p.MoveTo(render.Point{X: x, Y: br.Max.Y}).LineTo(render.Point{X: x + br.Dy(), Y: br.Min.Y})
				}
				f.Path(&p, render.Style{Stroke: hatch, Width: 1})
				f.Pop()
				if run := bandRuns[i]; run.Advance > 0 && run.Advance < br.Dx() {
					f.Glyphs(run, render.Point{X: (br.Min.X + br.Max.X - run.Advance) / 2, Y: br.Min.Y + run.Ascent + 3*k}, axisFg)
				}
			}
			if axis {
				for t, run := range tickRuns {
					y := gm.r.Max.Y - float32(t)/float32(o.YTicks)*gm.r.Dy()
					f.Rect(render.XYWH(gm.r.Min.X, y, gm.r.Dx(), max(1, k)/2), render.Style{Fill: grid})
					f.Glyphs(run, render.Point{X: r.Min.X + chartAxisW*k - 6*k - run.Advance, Y: min(y+run.Ascent/2-1, gm.r.Max.Y-2)}, axisFg)
				}
			} else if !spark {
				f.Rect(render.XYWH(gm.r.Min.X, gm.r.Max.Y-.5, gm.r.Dx(), .5), render.Style{Fill: grid})
			}
			drawSeries(f, gm, series, o, hl)
			for i, m := range o.Marks {
				x := gm.x(m.At)
				tone := m.Tone
				if tone.A == 0 {
					tone = markFg
				}
				if m.Dashed {
					f.Path(render.Dashed([]render.Point{{X: x, Y: gm.r.Min.Y}, {X: x, Y: gm.r.Max.Y}}, 3*k, 3*k), render.Style{Stroke: tone, Width: 1})
				} else {
					f.Rect(render.XYWH(x-.5, gm.r.Min.Y, 1, gm.r.Dy()), render.Style{Fill: tone})
				}
				if run := markRuns[i]; run.Advance > 0 {
					lx := min(x+3*k, gm.r.Max.X-run.Advance)
					f.Glyphs(run, render.Point{X: lx, Y: gm.r.Min.Y + run.Ascent + 1}, tone)
				}
			}
		})
		if len(o.Ticks) > 0 {
			label := c.Shaper().Shape(o.TicksLabel, font)
			c.Paint("ticks", ui.Flex(1), ui.Px(10), func(f render.Frame, r render.Rect) {
				gm := g(r)
				if axis {
					f.Glyphs(label, render.Point{X: r.Min.X, Y: r.Min.Y + label.Ascent}, axisFg)
				}
				for _, t := range o.Ticks {
					x := gm.x(t)
					f.Rect(render.XYWH(x-.5, r.Min.Y+2*k, 1, 6*k), render.Style{Fill: axisFg})
				}
			})
		}
		if len(o.Labels) > 0 && !spark {
			every := o.LabelEvery
			if every <= 0 {
				every = max(1, int(math.Ceil(float64(n)*54/float64(max(prev.Dx(), 1)))))
			}
			runs := make([]*typeset.Run, len(o.Labels))
			for i, l := range o.Labels {
				if i%every == 0 && strings.TrimSpace(l) != "" {
					runs[i] = c.Shaper().Shape(l, font)
				}
			}
			c.Paint("labels", ui.Flex(1), ui.Px(12), func(f render.Frame, r render.Rect) {
				gm := g(r)
				for i, run := range runs {
					if run == nil {
						continue
					}
					x := min(max(gm.x(float64(i))-run.Advance/2, r.Min.X), r.Max.X-run.Advance)
					f.Glyphs(run, render.Point{X: x, Y: r.Min.Y + run.Ascent + 2}, axisFg)
				}
			})
		}
	})
	if !in.Inside && !in.Hovered {
		return res
	}
	if prev.Dx() <= 0 {
		return res
	}
	canvas := prev
	canvas.Max.Y = canvas.Min.Y + h*k
	gm := g(canvas)
	p := in.Pointer()
	if p.X < gm.r.Min.X || p.X > gm.r.Max.X || p.Y < gm.r.Min.Y || p.Y > gm.r.Max.Y || n == 0 {
		return res
	}
	res.Point = gm.index(p.X)
	res.Series = seriesAt(gm, series, o, res.Point, p.Y)
	res.Clicked = in.Clicked
	if o.Hover && !spark {
		chartCard(c, series, o, res.Point, p)
	}
	return res
}

// seriesAt is the series at y on point i: the stack segment under it, else the nearest line within a few pixels.
func seriesAt(g chartGeom, series []ChartSeries, o ChartOpts, i int, y float32) int {
	stack := 0.0
	for s, x := range series {
		k := x.kindIn(o)
		if k != ChartBars && k != ChartArea {
			continue
		}
		v := value(x, i)
		if v > 0 && y <= g.y(stack) && y >= g.y(stack+v) {
			return s
		}
		stack += v
	}
	best, dist := -1, float32(8)
	for s, x := range series {
		if k := x.kindIn(o); k != ChartLine && k != ChartSpark || i < x.From || i >= len(x.Values) || math.IsNaN(x.Values[i]) {
			continue
		}
		if d := float32(math.Abs(float64(g.y(x.Values[i]) - y))); d < dist {
			best, dist = s, d
		}
	}
	return best
}

func drawSeries(f render.Frame, g chartGeom, series []ChartSeries, o ChartOpts, hl int) {
	base := make([]float64, g.n)
	for si, s := range series {
		on := hl < 0 || hl == si
		tone := dim(s.Tone, on)
		switch s.kindIn(o) {
		case ChartBars:
			bw := max(g.slot*.72, 1)
			for i := range g.n {
				v := value(s, i)
				if v <= 0 {
					continue
				}
				y0, y1 := g.y(base[i]), g.y(base[i]+v)
				x := g.r.Min.X + float32(i)*g.slot + (g.slot-bw)/2
				f.Rect(render.Rect{Min: render.Point{X: x, Y: y1}, Max: render.Point{X: x + bw, Y: y0}}, render.Style{Fill: tone})
				base[i] += v
			}
		case ChartArea:
			var p render.Path
			for i := range g.n {
				pt := render.Point{X: g.x(float64(i)), Y: g.y(base[i] + value(s, i))}
				if i == 0 {
					p.MoveTo(pt)
				} else {
					p.LineTo(pt)
				}
			}
			for i := g.n - 1; i >= 0; i-- {
				p.LineTo(render.Point{X: g.x(float64(i)), Y: g.y(base[i])})
			}
			p.Close()
			f.Path(&p, render.Style{Fill: alpha(tone, .35)})
			var edge []render.Point
			for i := range g.n {
				base[i] += value(s, i)
				edge = append(edge, render.Point{X: g.x(float64(i)), Y: g.y(base[i])})
			}
			f.Path(polyline(edge), render.Style{Stroke: tone, Width: 1.5})
		case ChartLine, ChartSpark:
			var run []render.Point
			flush := func() {
				if len(run) > 1 {
					if s.Dashed {
						f.Path(render.Dashed(run, 4, 3), render.Style{Stroke: tone, Width: 1.5})
					} else {
						f.Path(polyline(run), render.Style{Stroke: tone, Width: 1.5})
					}
				}
				run = run[:0]
			}
			for i := s.From; i < len(s.Values) && i < max(g.n, len(s.Values)); i++ {
				if math.IsNaN(s.Values[i]) {
					flush()
					continue
				}
				run = append(run, render.Point{X: g.x(float64(i)), Y: g.y(s.Values[i])})
			}
			flush()
		}
	}
}

func polyline(pts []render.Point) *render.Path {
	var p render.Path
	for i, pt := range pts {
		if i == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	return &p
}

// chartCard is the hover card: the point's title and each series' value there.
func chartCard(c *ui.Ctx, series []ChartSeries, o ChartOpts, i int, at render.Point) {
	format := o.Format
	if format == nil {
		format = func(v float64) string { return fmt.Sprintf("%.2f", v) }
	}
	title := ""
	switch {
	case o.Title != nil:
		title = o.Title(i)
	case i < len(o.Labels):
		title = o.Labels[i]
	}
	type row struct {
		name string
		tone render.Color
		v    float64
	}
	var rows []row
	total := 0.0
	for _, s := range series {
		if i < len(s.Values) && !math.IsNaN(s.Values[i]) && s.Values[i] != 0 && i >= s.From {
			rows = append(rows, row{s.Name, s.Tone, s.Values[i]})
			total += s.Values[i]
		}
	}
	if len(rows) == 0 && title == "" {
		return
	}
	if o.kind() == ChartBars || o.kind() == ChartArea {
		title = strings.TrimSpace(title + " · " + format(total))
	}
	pos := render.Point{X: at.X + 14, Y: at.Y - 10}
	c.Column("card", ui.Style{Pad: ui.PadXY(9, 6), Gap: 2, Fill: col(c, tMenuBg), Stroke: col(c, tMenuBorder), Border: 1, Radius: 6,
		Z: ui.LayerPopup, Float: &ui.Float{Point: &pos}, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Text("t", title, monoText(10.5, typeset.SemiBold, col(c, tMenuFg), 0))
		for j, r := range rows {
			c.Row(j, ui.Style{Gap: 6, Cross: ui.Center, W: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Box("sw", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 2, Fill: r.tone}, nil)
				c.Text("n", r.name, monoText(10.5, typeset.Normal, col(c, tChartLabel), 0))
				c.Box("gap", ui.Style{W: ui.Flex(1), H: ui.Px(1)}, nil)
				c.Box("pad", ui.Style{W: ui.Px(10)}, nil)
				c.Text("v", format(r.v), monoText(10.5, typeset.Medium, col(c, tMenuFg), 0))
			})
		}
	})
}

// chartTable is the chart's data as a table: a row per point, a column per series.
func chartTable(c *ui.Ctx, series []ChartSeries, o ChartOpts) {
	format := o.Format
	if format == nil {
		format = func(v float64) string { return fmt.Sprintf("%.2f", v) }
	}
	head := monoText(9, typeset.SemiBold, col(c, tChartAxis), .1)
	cell := monoText(10.5, typeset.Normal, col(c, tChartLabel), 0)
	c.Column("table", ui.Style{W: ui.Flex(1), Role: ui.RoleList, Label: o.Label}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(18), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("x", ui.Style{W: ui.Flex(1)}, nil)
			for i, s := range series {
				c.Row(i, ui.Style{W: ui.Px(90), Main: ui.End}, func(c *ui.Ctx) { c.Text("t", strings.ToUpper(s.Name), head) })
			}
		})
		for p := range points(series) {
			label := fmt.Sprint(p + 1)
			if p < len(o.Labels) {
				label = o.Labels[p]
			}
			c.Row(p, ui.Style{W: ui.Flex(1), H: ui.Px(18), Cross: ui.Center, Role: ui.RoleListItem, Label: label}, func(c *ui.Ctx) {
				c.Row("x", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { c.Text("t", label, cell) })
				for i, s := range series {
					v := ""
					if p < len(s.Values) && !math.IsNaN(s.Values[p]) {
						v = format(s.Values[p])
					}
					c.Row(i, ui.Style{W: ui.Px(90), Main: ui.End}, func(c *ui.Ctx) { c.Text("t", v, cell) })
				}
			})
		}
	})
}
