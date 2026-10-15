package api

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

func coreSession(id string) core.SessionID { return core.SessionID(id) }

//oriel:api name=ui cap=ui
type UI struct{ a *APIs }

//oriel:api doc="A row of nodes; options beside them: gap, pad (px). Any node can be dragged (drag = { kind, data, label }) or take drops (accepts = { kinds }, on_drop = fn(kind, data); a 'file' drop's data is its paths)." returns=table
func (UI) Row(children any) script.Table { return container("row", children) }

//oriel:api doc="A column of nodes; options beside them: gap, pad (px)." returns=table
func (UI) Column(children any) script.Table { return container("column", children) }

// container is a row or column from Lua's table: its list part the children, its named fields the options.
func container(kind string, v any) script.Table {
	t := script.Table{"type": kind}
	switch v := v.(type) {
	case []any:
		t["children"] = v
	case script.Table:
		var kids []any
		for i := 1; ; i++ {
			x, ok := v[strconv.Itoa(i)]
			if !ok {
				break
			}
			kids = append(kids, x)
		}
		t["children"] = kids
		for k, x := range v {
			if _, err := strconv.Atoi(k); err != nil {
				t[k] = x
			}
		}
	}
	return t
}

//oriel:api doc="Text, in a style: dim, faint, accent, warn, error, ok, or title (larger and bold: a heading, a meeting's name)." optional=style returns=table
func (UI) Text(s any, style string) script.Table {
	text := ""
	if s != nil {
		text = fmt.Sprint(s)
	}
	return script.Table{"type": "text", "text": text, "style": style}
}

//oriel:api doc="Space that takes what's left." returns=table
func (UI) Spacer() script.Table { return script.Table{"type": "spacer"} }

//oriel:api doc="The editor's mode, as the built-in status line shows it." returns=table
func (UI) Mode() script.Table { return script.Table{"type": "view", "id": "statusline.left.00-mode"} }

//oriel:api doc="An indicator: { label, value, bar (0 to 1), warn (the bar's warning level), tone (ok, warn, error), dot (a status dot before the value: ok, warn, error, accent, dim), id }." returns=table
func (UI) Indicator(spec script.Table) script.Table {
	t := script.Table{"type": "indicator"}
	for k, v := range spec {
		t[k] = v
	}
	return t
}

//oriel:api doc="A dial (widget.Ring): { value (0 to 1 of the ring lit, clockwise from twelve o'clock), label (in the middle: '18m'), color (a theme token or #rrggbb; the accent by default), size (px across, 44 by default), name (what it measures, read out with the label), id }." returns=table
func (UI) Ring(spec script.Table) script.Table { return node("ring", spec) }

//oriel:api doc="A button: { label, on_click = fn(), primary, id }. The id keeps it the same button as the view redraws, where labels repeat or change." returns=table
func (UI) Button(spec script.Table) script.Table {
	t := script.Table{"type": "button"}
	for k, v := range spec {
		t[k] = v
	}
	return t
}

//oriel:api doc="A text field: { value, placeholder, on_change = fn(text), on_submit = fn(text), id, label, lines }: lines > 1 takes several lines (⏎ a new line, ⌘⏎ submits). Keep the text in oriel.ui.state and give it back as value." returns=table
func (UI) Input(spec script.Table) script.Table { return node("input", spec) }

//oriel:api doc="An on/off switch: { on, on_change = fn(on), id, label }." returns=table
func (UI) Toggle(spec script.Table) script.Table { return node("toggle", spec) }

//oriel:api doc="A menu of choices: { value, options = { … }, on_change = fn(value), id, label }." returns=table
func (UI) Select(spec script.Table) script.Table { return node("select", spec) }

//oriel:api doc="A range of days (widget.DatePicker): { from, to, on_change = fn(from, to), placeholder, id, label }. Days are YYYY-MM-DD; a button showing the range opens a calendar under it, where arrows move, Enter picks one end then the other, Esc cancels, and a range can be typed as 2026-09-01..2026-09-30." returns=table
func (UI) DatePicker(spec script.Table) script.Table { return node("date_picker", spec) }

//oriel:api doc="A table kept by key across redraws and calls, made from initial the first time: a view's own state. Include ctx.instance in the key for one per instance." optional=initial returns=table
func (u UI) State(key string, initial any) any { return u.a.VM.State(key, initial) }

func node(kind string, spec script.Table) script.Table {
	t := script.Table{"type": kind}
	for k, v := range spec {
		t[k] = v
	}
	return t
}

//oriel:api doc="A chart (widget.Chart): { kind = bars|area|line|spark|strip, series = { { name, values = { … }, color, kind, dashed, from } }, labels, height, max, y_ticks, format = money|percent|count, legend, hover, marks = { { at, label, color, dashed } }, bands = { { from, to, label } }, on_click = fn(series, point), id, label }. color is a theme token (cyan, accent, stats.kind.output) or #rrggbb; series and points count from 1." returns=table
func (UI) Chart(spec script.Table) script.Table { return node("chart", spec) }

//oriel:api doc="A list: each of items through row(item), which returns a node." returns=table
func (UI) List(items any, row *script.Fn) script.Table {
	return script.Table{"type": "list", "items": items, "row": row}
}

var styles = map[string]theme.TokenID{"dim": theme.FgMuted, "faint": theme.FgFaint, "accent": theme.Accent, "warn": theme.Warn,
	"error": theme.Error, "ok": theme.OK}

// buildView adds a view's nodes to c: a single line of them (a row, text, a indicator, a button) is centred in the height
// the view is given, as a bar's segment is; a column runs from the top, as a panel does.
func buildView(c *ui.Ctx, v any, a *APIs) {
	if t, ok := v.(script.Table); ok && str(t, "type") == "column" {
		build(c, v, a)
		return
	}
	c.Row(nil, ui.Style{H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { build(c, v, a) })
}

// build adds a node table, as oriel.ui makes them, to c.
func build(c *ui.Ctx, v any, a *APIs) {
	th := c.Theme()
	switch n := v.(type) {
	case nil:
	case string:
		c.Text(nil, n, ui.TextStyle{})
	case []any:
		c.Row(nil, ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
			for _, x := range n {
				build(c, x, a)
			}
		})
	case script.Table:
		if dnd(c, n, a) {
			return
		}
		switch str(n, "type") {
		case "row", "column":
			st := ui.Style{Gap: 12, Cross: ui.Center, W: ui.Flex(1)}
			if str(n, "type") == "column" {
				st = ui.Style{Dir: ui.Column, Gap: 4, W: ui.Flex(1)}
			}
			if g, ok := n["gap"].(float64); ok {
				st.Gap = float32(g)
			}
			if p, ok := n["pad"].(float64); ok {
				st.Pad = ui.Pad(float32(p))
			}
			c.Box(nil, st, func(c *ui.Ctx) {
				kids, _ := n["children"].([]any)
				for _, x := range kids {
					build(c, x, a)
				}
			})
		case "text":
			col := th.C(theme.Fg)
			if tok, ok := styles[str(n, "style")]; ok {
				col = th.C(tok)
			}
			font := typeset.FontSpec{Family: typeset.Mono, Size: 11}
			if str(n, "style") == "title" {
				font = typeset.FontSpec{Family: typeset.UI, Size: 14, Weight: typeset.SemiBold}
			}
			c.Text(nil, str(n, "text"), ui.TextStyle{Font: font, Color: col})
		case "ring":
			size, _ := n["size"].(float64)
			widget.Ring(c, cmpOr(str(n, "id"), str(n, "name"), "ring"), widget.RingOpts{Frac: num(n["value"]), Label: str(n, "label"),
				Tone: tone(th, str(n, "color"), render.Color{}), Size: float32(size), Name: str(n, "name"), Help: "plugins/lua#ui"})
		case "spacer":
			c.Box(nil, ui.Style{W: ui.Flex(1)}, nil)
		case "view":
			if view, ok := a.Host.ViewByID(str(n, "id")); ok {
				c.Node(view.Build(registry.ViewCtx{}))
			}
		case "indicator":
			indicator(c, n, th)
		case "button":
			key := cmpOr(str(n, "id"), str(n, "label"))
			primary, _ := n["primary"].(bool)
			if widget.Button(c, key, str(n, "label"), widget.ButtonOpts{Primary: primary, Help: "plugins/lua#ui"}) {
				a.callback(n, "on_click")
			}
		case "input":
			key := cmpOr(str(n, "id"), str(n, "label"), "input")
			text := str(n, "value")
			c.Box(key, ui.Style{W: ui.Px(220)}, func(c *ui.Ctx) {
				lines, _ := n["lines"].(float64)
				res := widget.Input(c, "in", &text, widget.InputOpts{Label: cmpOr(str(n, "label"), key), Placeholder: str(n, "placeholder"),
					Help: "plugins/lua#ui", TabMoves: true, Lines: int(lines)})
				if res.Changed {
					a.callback(n, "on_change", text)
				}
				if res.Submitted {
					a.callback(n, "on_submit", text)
				}
			})
		case "toggle":
			key := cmpOr(str(n, "id"), str(n, "label"), "toggle")
			on, _ := n["on"].(bool)
			if widget.Toggle(c, key, on, widget.ToggleOpts{Label: cmpOr(str(n, "label"), key), Help: "plugins/lua#ui"}) {
				a.callback(n, "on_change", !on)
			}
		case "select":
			key := cmpOr(str(n, "id"), str(n, "label"), "select")
			if v, ok := widget.Select(c, key, str(n, "value"), strs(n["options"]), widget.SelectOpts{Label: cmpOr(str(n, "label"), key),
				Help: "plugins/lua#ui"}); ok {
				a.callback(n, "on_change", v)
			}
		case "chart":
			chart(c, n, a)
		case "date_picker":
			key := cmpOr(str(n, "id"), str(n, "label"), "date_picker")
			var r widget.DateRange
			if v, err := widget.ParseDateRange(str(n, "from")+".."+str(n, "to"), time.Local); err == nil {
				r = v
			}
			if v, ok := widget.DateSelect(c, key, r, widget.DateSelectOpts{Label: cmpOr(str(n, "label"), key),
				Placeholder: cmpOr(str(n, "placeholder"), "pick dates"), Help: "plugins/lua#ui"}); ok {
				a.callback(n, "on_change", v.From.Format(time.DateOnly), v.To.Format(time.DateOnly))
			}
		case "list":
			row, _ := n["row"].(*script.Fn)
			items, _ := n["items"].([]any)
			c.Column(nil, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
				for _, it := range items {
					if row == nil {
						build(c, it, a)
						continue
					}
					out, err := a.VM.Call(row, it)
					if err != nil {
						panic(err)
					}
					if len(out) > 0 {
						build(c, out[0], a)
					}
				}
			})
		}
	default:
		c.Text(nil, fmt.Sprint(v), ui.TextStyle{})
	}
}

// dnd builds a node that's a drag source (drag = { kind, data, label }) or a drop target (accepts = { kinds },
// on_drop = fn(kind, data)) inside a box that is one, and reports whether it did.
func dnd(c *ui.Ctx, n script.Table, a *APIs) bool {
	spec, _ := n["drag"].(script.Table)
	accepts := strs(n["accepts"])
	if spec == nil && len(accepts) == 0 {
		return false
	}
	key := cmpOr(str(n, "id"), str(n, "label"), "dnd")
	id := c.Next(key)
	if d := c.Interaction(id).Dropped; d != nil {
		a.callback(n, "on_drop", d.Kind, d.Data)
	}
	st := ui.Style{W: ui.Flex(1), Drop: accepts, Help: "plugins/lua#ui", Role: ui.RoleGroup, Label: cmpOr(str(n, "label"), key)}
	if spec != nil {
		st.Drag = &ui.DragData{Kind: str(spec, "kind"), Data: spec["data"], Label: cmpOr(str(spec, "label"), str(spec, "kind"))}
		st.Label = st.Drag.Label
	}
	inner := script.Table{}
	for k, v := range n {
		if k != "drag" && k != "accepts" && k != "on_drop" {
			inner[k] = v
		}
	}
	c.Box(key, st, func(c *ui.Ctx) { build(c, inner, a) })
	return true
}

// callback calls a node's function field with args, if it has one, and redraws: an error is a notice at its line,
// as PL-12's are.
func (a *APIs) callback(n script.Table, field string, args ...any) {
	fn, ok := n[field].(*script.Fn)
	if !ok {
		return
	}
	if _, err := a.VM.Call(fn, args...); err != nil {
		nt := core.Notice{ID: "lua.callback." + fn.Source, Severity: core.SevError, Source: fn.Source, Title: "Lua", Text: err.Error()}
		if e, ok := err.(*script.Error); ok {
			nt.Location, nt.Detail = e.Where(), e.Trace()
		}
		a.Host.Notify(nt)
	}
	a.Host.Redraw()
}

// indicator is a label, a bar and a value, as the HUD and status line draw them (widget.Indicator).
func indicator(c *ui.Ctx, n script.Table, th *theme.Resolved) {
	o := widget.IndicatorOpts{Label: str(n, "label"), Help: "plugins/lua#ui"}
	switch str(n, "tone") {
	case "warn":
		o.Tone = th.C(theme.Warn)
	case "error":
		o.Tone = th.C(theme.Error)
	case "ok":
		o.Tone = th.C(theme.OK)
	}
	if bar, ok := n["bar"].(float64); ok {
		o.Meter, o.Frac = true, max(0, min(1, bar))
		o.Warn, _ = n["warn"].(float64)
	}
	if v, ok := n["value"]; ok && v != nil {
		o.Value = fmt.Sprint(v)
	}
	if d := str(n, "dot"); d != "" {
		if tok, ok := styles[d]; ok {
			o.Dot = th.C(tok)
		}
	}
	widget.Indicator(c, cmpOr(str(n, "id"), str(n, "label"), "indicator"), o)
}

// toTable is an event as Lua sees it: a struct's fields under snake_case names, or a map as it is.
func toTable(ev any) script.Table {
	t := script.Table{}
	if ev == nil {
		return t
	}
	if m, ok := ev.(map[string]any); ok {
		for k, v := range m {
			t[k] = v
		}
		return t
	}
	rv := reflect.Indirect(reflect.ValueOf(ev))
	if rv.Kind() != reflect.Struct {
		return t
	}
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		if !f.IsExported() {
			continue
		}
		t[snake(f.Name)] = rv.Field(i).Interface()
	}
	return t
}

// fromTable writes a rewritten event back: each field the table has, into the struct (or map) ev points to.
func fromTable(t script.Table, ev any) {
	if m, ok := ev.(map[string]any); ok {
		for k, v := range t {
			m[k] = v
		}
		return
	}
	rv := reflect.ValueOf(ev)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return
	}
	rv = rv.Elem()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Type().Field(i)
		v, ok := t[snake(f.Name)]
		if !ok || !f.IsExported() {
			continue
		}
		fv := rv.Field(i)
		val := reflect.ValueOf(v)
		switch {
		case !val.IsValid():
			fv.Set(reflect.Zero(fv.Type()))
		case val.Type().ConvertibleTo(fv.Type()):
			fv.Set(val.Convert(fv.Type()))
		}
	}
}

// snake is a Go field name as Lua writes it: SessionID → session_id.
func snake(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1])) {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// items are a Lua list: a table's 1, 2, 3… or a Go slice.
func items(v any) []any {
	switch v := v.(type) {
	case []any:
		return v
	case script.Table:
		var out []any
		for i := 1; ; i++ {
			x, ok := v[strconv.Itoa(i)]
			if !ok {
				return out
			}
			out = append(out, x)
		}
	}
	return nil
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

var chartKinds = map[string]widget.ChartKind{"bars": widget.ChartBars, "area": widget.ChartArea, "line": widget.ChartLine,
	"spark": widget.ChartSpark, "strip": widget.ChartStrip}

var chartFormats = map[string]func(float64) string{
	"money":   func(v float64) string { return fmt.Sprintf("$%.2f", v) },
	"percent": func(v float64) string { return fmt.Sprintf("%.0f%%", v) },
	"count":   func(v float64) string { return fmt.Sprintf("%.0f", v) },
}

// tone is a colour from Lua: a theme token's name, or #rrggbb.
func tone(th *theme.Resolved, v string, fallback render.Color) render.Color {
	if v == "" {
		return fallback
	}
	if c, ok := th.Color(v); ok {
		return c
	}
	if c, err := theme.ParseColor(v); err == nil {
		return c
	}
	return fallback
}

// chart builds an oriel.ui.chart node with widget.Chart, as the built-in pages draw theirs.
func chart(c *ui.Ctx, n script.Table, a *APIs) {
	th := c.Theme()
	palette := []theme.TokenID{theme.Accent, theme.OK, theme.Warn, theme.Error, theme.StatusCI, theme.StatusReviewer}
	var series []widget.ChartSeries
	for i, x := range items(n["series"]) {
		t, ok := x.(script.Table)
		if !ok {
			continue
		}
		s := widget.ChartSeries{Name: str(t, "name"), Tone: tone(th, str(t, "color"), th.C(palette[i%len(palette)])), Kind: chartKinds[str(t, "kind")]}
		for _, v := range items(t["values"]) {
			s.Values = append(s.Values, num(v))
		}
		s.Dashed, _ = t["dashed"].(bool)
		if f := int(num(t["from"])); f > 0 {
			s.From = f - 1
		}
		series = append(series, s)
	}
	o := widget.ChartOpts{Kind: chartKinds[str(n, "kind")], Height: float32(num(n["height"])), Max: num(n["max"]), YTicks: int(num(n["y_ticks"])),
		Format: chartFormats[str(n, "format")], Highlight: -1, Label: cmpOr(str(n, "label"), "chart"), Help: "plugins/lua#ui"}
	o.YFormat = o.Format
	o.Legend, _ = n["legend"].(bool)
	o.Hover, _ = n["hover"].(bool)
	o.Labels = strs(n["labels"])
	for _, x := range items(n["marks"]) {
		if t, ok := x.(script.Table); ok {
			d, _ := t["dashed"].(bool)
			o.Marks = append(o.Marks, widget.ChartMark{At: num(t["at"]) - 1, Label: str(t, "label"), Tone: tone(th, str(t, "color"), render.Color{}), Dashed: d})
		}
	}
	for _, x := range items(n["bands"]) {
		if t, ok := x.(script.Table); ok {
			o.Bands = append(o.Bands, widget.ChartBand{From: num(t["from"]) - 1, To: num(t["to"]) - 1, Label: str(t, "label")})
		}
	}
	key := cmpOr(str(n, "id"), str(n, "label"), "chart")
	if res := widget.Chart(c, key, series, o); res.Clicked {
		a.callback(n, "on_click", float64(res.Series+1), float64(res.Point+1))
	}
}
