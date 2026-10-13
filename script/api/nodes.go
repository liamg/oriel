package api

import (
	"fmt"
	"strconv"
	"reflect"
	"strings"
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

//oriel:api name=ui
type UI struct{ a *APIs }

//oriel:api doc="A row of nodes; options beside them: gap, pad (px)." returns=table
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

//oriel:api doc="Text, in a style: dim, faint, accent, warn, error or ok." optional=style returns=table
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

//oriel:api doc="A tat: { label, value, bar (0 to 1), warn (the bar's warning level), tone (ok, warn, error) }." returns=table
func (UI) Tat(spec script.Table) script.Table {
	t := script.Table{"type": "tat"}
	for k, v := range spec {
		t[k] = v
	}
	return t
}

//oriel:api doc="A button: { label, on_click = fn(), primary, id }. The id keeps it the same button as the view redraws, where labels repeat or change." returns=table
func (UI) Button(spec script.Table) script.Table {
	t := script.Table{"type": "button"}
	for k, v := range spec {
		t[k] = v
	}
	return t
}

//oriel:api doc="A list: each of items through row(item), which returns a node." returns=table
func (UI) List(items any, row *script.Fn) script.Table {
	return script.Table{"type": "list", "items": items, "row": row}
}

var styles = map[string]theme.TokenID{"dim": theme.FgMuted, "faint": theme.FgFaint, "accent": theme.Accent, "warn": theme.Warn,
	"error": theme.Error, "ok": theme.OK}

// buildView adds a view's nodes to c: a single line of them (a row, text, a tat, a button) is centred in the height
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
			c.Text(nil, str(n, "text"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: col})
		case "spacer":
			c.Box(nil, ui.Style{W: ui.Flex(1)}, nil)
		case "view":
			if view, ok := a.Host.ViewByID(str(n, "id")); ok {
				c.Node(view.Build(registry.ViewCtx{}))
			}
		case "tat":
			tat(c, n, th)
		case "button":
			key := cmpOr(str(n, "id"), str(n, "label"))
			primary, _ := n["primary"].(bool)
			if widget.Button(c, key, str(n, "label"), widget.ButtonOpts{Primary: primary, Help: "plugins/lua"}) {
				if fn, ok := n["on_click"].(*script.Fn); ok {
					if _, err := a.VM.Call(fn); err != nil {
						a.Host.Notify(core.Notice{ID: "lua.button", Severity: core.SevError, Title: "Lua", Text: err.Error()})
					}
					a.Host.Redraw()
				}
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

// tat is a label, a bar and a value, as the HUD and status line draw them.
func tat(c *ui.Ctx, n script.Table, th *theme.Resolved) {
	mono := func(tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(tok)}
	}
	tone := theme.Accent
	switch str(n, "tone") {
	case "warn":
		tone = theme.Warn
	case "error":
		tone = theme.Error
	case "ok":
		tone = theme.OK
	}
	c.Row(nil, ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("label", str(n, "label"), mono(theme.FgMuted))
		if bar, ok := n["bar"].(float64); ok {
			if warn, ok := n["warn"].(float64); ok && bar >= warn {
				tone = theme.Warn
			}
			fill := th.C(tone)
			track := th.C(theme.BgStrong)
			c.Paint("bar", ui.Px(48), ui.Px(4), func(f render.Frame, r render.Rect) {
				f.Rect(r, render.Style{Fill: track, Radius: 2})
				w := r.Dx() * float32(max(0, min(1, bar)))
				f.Rect(render.XYWH(r.Min.X, r.Min.Y, w, r.Dy()), render.Style{Fill: fill, Radius: 2})
			})
			c.Text("pct", fmt.Sprintf("%d%%", int(bar*100+0.5)), mono(theme.Fg))
		}
		if v, ok := n["value"]; ok {
			c.Text("value", fmt.Sprint(v), mono(tone))
		}
	})
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
