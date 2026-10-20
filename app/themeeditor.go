package app

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io/fs"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/scenes"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tThemePick     = theme.Declare("theme.pick", "status.reviewer")
	tThemePickInk  = theme.Declare("theme.pick.ink", "bg.base")
	tThemePanel    = theme.Declare("theme.panel.bg", "bg.surface")
	tThemeStage    = theme.Declare("theme.stage.bg", "bg.base")
	tThemeMine     = theme.Declare("theme.override", "accent")
	tThemeWritten  = theme.Declare("theme.written", "ok")
	tThemeTOMLKey  = theme.Declare("theme.toml.key", "fg.muted")
	tThemeTOMLHead = theme.Declare("theme.toml.table", "status.permission")
	tThemeTOMLVal  = theme.Declare("theme.toml.value", "syn.string")
)

// themeEditor is :Theme (theming.md, 23-theme): every token, a preview of the fixture workspace, the picked token's
// chain, its value as an expression or in OKLCH, contrast and lint, and the theme file as it's written. Pick mode
// reads what painted a pixel off the window drawn in a probe theme. Built only on the public API.
type themeEditor struct {
	disposer
	h   plugin.Host
	dir string // the user's themes directory, where a local theme goes

	open    bool
	picking bool
	probe   *image.RGBA // the window in probeTh, taken as pick mode starts
	probeTh *theme.Resolved
	hoverAt render.Point
	hover   string
	hoverR  render.Rect

	picked string // what was picked: a token, "palette:x" or "@capture"
	sel    string // the link of its chain being edited
	loaded string // the link the value fields were loaded from
	filter string
	tab    int // preview: fixture, or split against the base theme
	mode   int // value: derive or fixed
	expr   string
	hex    string
	lch    theme.OKLCH

	undo    [][]byte
	undoKey string // the name the last undo entry was for, so a drag is one entry
	written string // the name last written, lit in the TOML strip
	live    map[string]*liveFile

	fixtures map[*theme.Resolved]*fixture
	lint     []theme.Finding
	lintTh   *theme.Resolved
	paintDur time.Duration
}

// fixture is the reference workspace (render/scenes) drawn in one theme, and its probe for picking.
type fixture struct {
	canvas *scenes.Canvas
	probe  *image.RGBA
}

func (e *themeEditor) Manifest() plugin.Manifest { return plugin.Manifest{ID: "theme"} }

func (e *themeEditor) Activate(_ context.Context, h plugin.Host) error {
	e.h = h
	e.live = map[string]*liveFile{}
	e.fixtures = map[*theme.Resolved]*fixture{}
	place := pagePlace("theme", "Theme editor", "theme.edit", &e.open)
	place.Shown = func() bool { return e.open || e.picking }
	e.add(h.Contribute(place))
	str := func(a registry.Args, k string) string { s, _ := a[k].(string); return strings.TrimSpace(s) }
	for _, c := range []registry.Command{
		{ID: "Theme", Title: "The theme editor; :Theme pick to pick from the window, :Theme {name} to switch",
			Run: func(_ registry.Ctx, a registry.Args) error {
				switch arg := str(a, "args"); arg {
				case "":
					e.open = true
				case "pick":
					return e.startPick()
				default:
					return h.Options().Set("ui.theme", registry.LayerUser, registry.At{}, arg)
				}
				return nil
			}},
		{ID: "theme.edit", Title: "Open the theme editor", Run: func(registry.Ctx, registry.Args) error {
			e.open = true
			return nil
		}},
		{ID: "theme.pick", Title: "Pick a token from the window", Run: func(registry.Ctx, registry.Args) error { return e.startPick() }},
		{ID: "theme.select", Title: "Edit a token: {name}", Run: func(_ registry.Ctx, a registry.Args) error {
			e.choose(str(a, "name"))
			e.open = true
			return nil
		}},
		{ID: "theme.set", Title: "Override a token in your theme: {name} = {value}", Run: func(_ registry.Ctx, a registry.Args) error {
			return e.set(str(a, "name"), str(a, "value"))
		}},
		{ID: "theme.reset", Title: "Reset a token to what it inherits: {name}, or the one being edited", Run: func(_ registry.Ctx, a registry.Args) error {
			name := str(a, "name")
			if name == "" {
				name = e.sel
			}
			return e.reset(name)
		}},
		{ID: "theme.undo", Title: "Undo the last theme edit", Run: func(registry.Ctx, registry.Args) error { return e.undoLast() }},
	} {
		e.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "theme", Keys: "p", Command: "theme.pick", Desc: "Pick from the window"},
		{Mode: "theme", Keys: "r", Command: "theme.reset", Desc: "Reset to inherited"},
		{Mode: "theme", Keys: "u", Command: "theme.undo", Desc: "Undo"},
		{Mode: "theme", Keys: "<D-z>", Command: "theme.undo", Desc: "Undo"},
	} {
		e.add(h.Keymap(k))
	}
	e.add(h.View(OverlayPrefix+"theme", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "theme", Fn: e.build}
	})))
	e.add(h.View(OverlayPrefix+"themepick", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "themepick", Fn: e.buildPick}
	})))
	return nil
}

// choose makes name the picked token, editing its own value.
func (e *themeEditor) choose(name string) {
	e.picked, e.sel, e.loaded = name, name, ""
}

// colorOf is a name's colour: a token, a palette colour, or a syntax group's foreground.
func colorOf(r *theme.Resolved, name string) render.Color {
	switch {
	case strings.HasPrefix(name, "palette:"):
		c, _ := r.PaletteColor(strings.TrimPrefix(name, "palette:"))
		return c
	case strings.HasPrefix(name, "@"):
		return r.Syntax(name).FG
	}
	c, _ := r.Color(name)
	return c
}

func exprOf(r *theme.Resolved, name string) string {
	if l, ok := r.Link(name); ok {
		return l.Expr
	}
	return ""
}

// --- the theme file ---

// liveFile is a theme file the editor writes: it repaints on the editor's own writes at once, not at the next poll.
type liveFile struct {
	theme.File
	mu   sync.Mutex
	subs map[int]func()
	n    int
}

func (f *liveFile) Watch(fn func()) func() {
	stop := f.File.Watch(fn)
	f.mu.Lock()
	f.n++
	id := f.n
	f.subs[id] = fn
	f.mu.Unlock()
	return func() {
		stop()
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
	}
}

func (f *liveFile) changed() {
	f.mu.Lock()
	subs := slices.Collect(maps.Values(f.subs))
	f.mu.Unlock()
	for _, fn := range subs {
		fn()
	}
}

func (e *themeEditor) themeName() string {
	if v, _, err := e.h.Options().Get("ui.theme", registry.At{}); err == nil {
		return v.(string)
	}
	return "oriel"
}

// target is the file edits go to: the current theme's own file, or else (a built-in, a plugin's) a local theme in
// the themes directory that extends it, made the current theme. v is the variant to write under: the current one
// when the theme has [dark] and [light] tables, else none.
func (e *themeEditor) target() (path string, v theme.Variant, err error) {
	name := e.themeName()
	srcs, _ := e.h.Themes()
	for i := len(srcs) - 1; i >= 0 && path == ""; i-- {
		if srcs[i].Name() != name {
			continue
		}
		switch s := srcs[i].(type) {
		case theme.File:
			path = s.Path
		case *liveFile:
			path = s.Path
		}
		break
	}
	if path == "" {
		local := name + "-local"
		path = filepath.Join(e.dir, local+".toml")
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(e.dir, 0o755); err != nil {
				return "", "", err
			}
			src := fmt.Sprintf("name = %q\nextends = %q\n", local, name)
			if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
				return "", "", err
			}
		}
		e.liveFor(path)
		if err := e.h.Options().Set("ui.theme", registry.LayerUser, registry.At{}, local); err != nil {
			return "", "", err
		}
		name = local
		srcs, _ = e.h.Themes()
	}
	e.liveFor(path)
	st, err := theme.Stack(srcs, name)
	if err != nil {
		return "", "", err
	}
	if slices.ContainsFunc(st, (*theme.Spec).HasVariants) {
		v = theme.Dark
		if r := e.h.CurrentTheme(); r != nil {
			v = r.Variant
		}
	}
	return path, v, nil
}

func (e *themeEditor) liveFor(path string) *liveFile {
	if f, ok := e.live[path]; ok {
		return f
	}
	f := &liveFile{File: theme.File{Path: path}, subs: map[int]func(){}}
	e.live[path] = f
	e.add(e.h.Theme(f))
	return f
}

// edit rewrites the target file with fn, keeping what it was for undo. With drag, edits to the same name in a row
// (a slider's) are one undo step.
func (e *themeEditor) edit(name string, drag bool, fn func(src []byte, v theme.Variant) ([]byte, error)) error {
	path, v, err := e.target()
	if err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := fn(src, v)
	if err != nil {
		return err
	}
	if string(out) == string(src) {
		return nil
	}
	if !drag || e.undoKey != name || len(e.undo) == 0 {
		e.undo = append(e.undo, src)
	}
	e.undoKey, e.written = "", name
	if drag {
		e.undoKey = name
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	e.liveFor(path).changed()
	return nil
}

func (e *themeEditor) set(name, expr string) error { return e.setting(name, expr, false) }

func (e *themeEditor) setting(name, expr string, drag bool) error {
	if name == "" {
		return fmt.Errorf("theme.set: no token")
	}
	if err := theme.CheckExpr(expr); err != nil {
		return err
	}
	return e.edit(name, drag, func(src []byte, v theme.Variant) ([]byte, error) { return theme.Override(src, v, name, expr) })
}

func (e *themeEditor) reset(name string) error {
	if name == "" {
		return nil
	}
	e.loaded = ""
	return e.edit("reset:"+name, false, func(src []byte, v theme.Variant) ([]byte, error) { return theme.Reset(src, v, name) })
}

func (e *themeEditor) undoLast() error {
	n := len(e.undo)
	if n == 0 {
		return nil
	}
	path, _, err := e.target()
	if err != nil {
		return err
	}
	src := e.undo[n-1]
	e.undo, e.undoKey, e.loaded = e.undo[:n-1], "", ""
	if err := os.WriteFile(path, src, 0o644); err != nil {
		return err
	}
	e.liveFor(path).changed()
	return nil
}

func (e *themeEditor) fail(err error) {
	if err != nil {
		e.h.Notify(core.Notice{ID: "theme.edit", Severity: core.SevError, Title: "Theme", Text: err.Error()})
	}
}

// --- pick mode ---

func (e *themeEditor) startPick() error {
	cur := e.h.CurrentTheme()
	if cur == nil {
		return fmt.Errorf("no theme to pick from yet")
	}
	wasOpen := e.open
	e.open = false
	e.probeTh = theme.Probe(cur)
	e.probe = e.h.Shot(e.probeTh)
	if e.probe == nil {
		e.open = wasOpen
		return fmt.Errorf("this window can't be drawn again to pick from")
	}
	e.picking, e.hover, e.hoverAt = true, "", render.Point{X: -1, Y: -1}
	return nil
}

// probeAt reads what painted p in img: the pixel's token, or the nearest one within a few pixels when p is on an
// antialiased edge. r is the extent of that token's paint around p.
func probeAt(img *image.RGBA, th *theme.Resolved, p image.Point) (string, image.Rectangle) {
	at := func(q image.Point) (string, bool) {
		if !q.In(img.Rect) {
			return "", false
		}
		c := img.RGBAAt(q.X, q.Y)
		return th.Probed(render.Color{R: c.R, G: c.G, B: c.B, A: 255})
	}
	for d := 0; d <= 4; d++ {
		for y := -d; y <= d; y++ {
			for x := -d; x <= d; x++ {
				if max(abs(x), abs(y)) != d {
					continue
				}
				q := p.Add(image.Pt(x, y))
				if name, ok := at(q); ok {
					return name, extent(img, q, 200_000)
				}
			}
		}
	}
	return "", image.Rectangle{}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// extent is the bounding box of the pixels the same colour as p, connected to it, up to limit of them.
func extent(img *image.RGBA, p image.Point, limit int) image.Rectangle {
	want := img.RGBAAt(p.X, p.Y)
	seen := map[image.Point]bool{p: true}
	queue := []image.Point{p}
	r := image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))}
	for len(queue) > 0 && len(seen) < limit {
		q := queue[0]
		queue = queue[1:]
		r = r.Union(image.Rectangle{Min: q, Max: q.Add(image.Pt(1, 1))})
		for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			n := q.Add(d)
			if !seen[n] && n.In(img.Rect) && img.RGBAAt(n.X, n.Y) == want {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return r
}

// buildPick is pick mode over the live window: what's under the pointer, outlined and named; a click picks it.
func (e *themeEditor) buildPick(c *ui.Ctx) {
	if !e.picking || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
	size := c.Size()
	id := c.Next("pick")
	in := c.Interaction(id)
	for _, k := range in.Keys {
		if k.Key == "Esc" {
			e.picking, e.probe = false, nil
			return
		}
	}
	if in.Pointer() != e.hoverAt {
		e.hoverAt = in.Pointer()
		name, r := probeAt(e.probe, e.probeTh, image.Pt(int(in.Pointer().X), int(in.Pointer().Y)))
		e.hover = name
		e.hoverR = render.Rect{Min: render.Point{X: float32(r.Min.X), Y: float32(r.Min.Y)}, Max: render.Point{X: float32(r.Max.X), Y: float32(r.Max.Y)}}
	}
	if in.Clicked && e.hover != "" {
		e.choose(e.hover)
		e.picking, e.probe, e.open = false, nil, true
		return
	}
	pink := th.C(tThemePick)
	c.Box("pick", ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Z: ui.LayerOverlay, Interactive: true, Focusable: true, KeyMode: "themepick",
		Cursor: render.CursorCrosshair, Float: &ui.Float{Point: &render.Point{}}, Help: "shell/theme#pick", Role: ui.RoleDialog,
		Label: "Pick mode: click anything"}, func(c *ui.Ctx) {
		if !in.Focused {
			c.Focus(id)
		}
		top := render.Point{X: size.X / 2, Y: 52}
		c.Row("banner", ui.Style{Pad: ui.PadXY(12, 5), Gap: 8, Cross: ui.Center, Fill: th.C(theme.BgBase), Stroke: pink, Border: 1, Radius: 12, Z: ui.LayerOverlay + 2,
			Float: &ui.Float{Point: &top, Self: render.Point{X: .5}}}, func(c *ui.Ctx) {
			c.Text("t", "PICK MODE · CLICK ANYTHING · ESC", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .1}, Color: pink})
		})
		if e.hover == "" {
			return
		}
		r := e.hoverR
		at := r.Min
		c.Box("outline", ui.Style{W: ui.Px(r.Dx() + 2), H: ui.Px(r.Dy() + 2), Stroke: pink, Border: 1, Radius: 3, Z: ui.LayerOverlay + 1,
			Float: &ui.Float{Point: &render.Point{X: at.X - 1, Y: at.Y - 1}}}, nil)
		tag := render.Point{X: at.X, Y: max(0, at.Y-18)}
		c.Row("tag", ui.Style{Pad: ui.PadXY(6, 2), Fill: pink, Radius: 3, Z: ui.LayerOverlay + 2, Float: &ui.Float{Point: &tag}}, func(c *ui.Ctx) {
			c.Text("n", e.hover, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.SemiBold}, Color: th.C(tThemePickInk)})
		})
	})
}

// --- the editor ---

func (e *themeEditor) build(c *ui.Ctx) {
	if !e.open || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
	if e.picked == "" {
		e.choose("accent")
	}
	widget.Page(c, "theme", &e.open, widget.PageOpts{Top: titleH, KeyMode: "theme", Label: "Theme editor", Help: "shell/theme"}, func(c *ui.Ctx) {
		c.Row("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			e.tokens(c, th)
			c.Box("d1", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("centre", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				e.toolbar(c, th)
				e.preview(c, th)
				c.Box("d2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				e.strip(c, th)
			})
			c.Box("d3", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			e.inspector(c, th)
		})
		e.footer(c, th)
	})
}

var (
	capLabel = func(th *theme.Resolved) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.Medium, Tracking: .14}, Color: th.C(theme.FgFaint)}
	}
	monoAt = func(th *theme.Resolved, size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
)

// row is a line of the token list: a group's heading, or a name with its swatch and expression.
type tokenRow struct {
	head, name, label, expr string
	color                   render.Color
	mine                    bool
}

func (e *themeEditor) rows(r *theme.Resolved) (rows []tokenRow, mine int) {
	own := "" // only a theme file of the user's has overrides of its own
	if path, _ := e.file(); path != "" {
		own = e.themeName()
	}
	f := strings.ToLower(e.filter)
	keep := func(name string) bool { return f == "" || strings.Contains(strings.ToLower(name), f) }
	add := func(name, label string, c render.Color) {
		if !keep(label) {
			return
		}
		l, _ := r.Link(name)
		row := tokenRow{name: name, label: label, expr: l.Expr, color: c, mine: own != "" && l.From == own}
		if row.mine {
			mine++
		}
		rows = append(rows, row)
	}
	head := func(h string) { rows = append(rows, tokenRow{head: h}) }
	head("PALETTE")
	for _, p := range r.Palette() {
		c, _ := r.PaletteColor(p)
		add("palette:"+p, p, c)
	}
	var sem, status, comp []theme.Token
	for _, t := range theme.Tokens() {
		switch {
		case strings.HasPrefix(t.Name, "status."):
			status = append(status, t)
		case theme.Section(t.Name)[0] == "semantic":
			sem = append(sem, t)
		default:
			comp = append(comp, t)
		}
	}
	for _, g := range []struct {
		head string
		ts   []theme.Token
	}{{"SEMANTIC", sem}, {"STATUS", status}, {fmt.Sprintf("COMPONENT  %d", len(comp)), comp}} {
		head(g.head)
		slices.SortFunc(g.ts, func(a, b theme.Token) int { return strings.Compare(a.Name, b.Name) })
		for _, t := range g.ts {
			add(t.Name, t.Name, r.C(t.ID))
		}
	}
	head("SYNTAX")
	for _, s := range r.SyntaxGroups() {
		add(s, s, r.Syntax(s).FG)
	}
	return rows, mine
}

// tokens is the left column: the theme, a filter, and every token by group.
func (e *themeEditor) tokens(c *ui.Ctx, th *theme.Resolved) {
	rows, mine := e.rows(th)
	c.Column("tokens", ui.Style{W: ui.Px(300), H: ui.Flex(1), Fill: th.C(tThemePanel)}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "THEME", capLabel(th))
			c.Text("name", e.themeName(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if base := e.base(); base != "" {
				c.Text("ext", "extends "+base, monoAt(th, 10, theme.FgFaint))
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("filter", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			widget.Input(c, "q", &e.filter, widget.InputOpts{Label: "Filter tokens", Placeholder: "filter tokens…", Help: "shell/theme#tokens"})
			c.Text("n", fmt.Sprintf("● %d overridden", mine), monoAt(th, 10, theme.FgFaint))
		})
		c.List("list", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, len(rows), 26, func(c *ui.Ctx, i int) {
			row := rows[i]
			if row.head != "" {
				c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(26), Pad: ui.PadXY(12, 0), Cross: ui.End}, func(c *ui.Ctx) {
					c.Text("h", row.head, capLabel(th))
				})
				return
			}
			fill := render.Color{}
			fg := theme.FgMuted
			if row.name == e.picked {
				fill, fg = th.C(theme.AccentBg), theme.Fg
			}
			in := widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(26), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center, Fill: fill,
				Help: "shell/theme#tokens", Role: ui.RoleListItem, Label: row.label, Selected: row.name == e.picked}, func(c *ui.Ctx) {
				dot := render.Color{}
				if row.mine {
					dot = th.C(tThemeMine)
				}
				c.Box("mine", ui.Style{W: ui.Px(4), H: ui.Px(4), Radius: 2, Fill: dot}, nil)
				c.Box("sw", ui.Style{W: ui.Px(14), H: ui.Px(14), Radius: 3, Fill: row.color, Stroke: th.C(theme.LineStrong), Border: 1}, nil)
				c.Text("n", row.label, monoAt(th, 11.5, fg))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("x", short(row.expr, 22), monoAt(th, 10, theme.FgGhost))
			})
			if in.Clicked {
				e.choose(row.name)
			}
		})
	})
}

func short(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// base is the theme the current one extends, if any.
func (e *themeEditor) base() string {
	srcs, _ := e.h.Themes()
	st, err := theme.Stack(srcs, e.themeName())
	if err != nil || len(st) < 2 {
		return ""
	}
	return st[len(st)-2].Name
}

func (e *themeEditor) toolbar(c *ui.Ctx, th *theme.Resolved) {
	c.Row("bar", ui.Style{W: ui.Flex(1), H: ui.Px(40), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		vs := "SPLIT VS BASE"
		if b := e.base(); b != "" {
			vs = "SPLIT VS " + strings.ToUpper(b)
		}
		widget.Segmented(c, "tab", []string{"FIXTURE", vs}, &e.tab, widget.SegmentedOpts{Help: "shell/theme#preview"})
		variant := 0
		if th.Variant == theme.Light {
			variant = 1
		}
		if widget.Segmented(c, "variant", []string{"DARK", "LIGHT"}, &variant, widget.SegmentedOpts{Help: "shell/theme#preview"}) {
			e.fail(e.h.Options().Set("ui.variant", registry.LayerUser, registry.At{}, []string{"dark", "light"}[variant]))
		}
		pink := th.C(tThemePick)
		if widget.Clickable(c, "pick", ui.Style{Dir: ui.Row, H: ui.Px(24), Pad: ui.PadXY(10, 0), Cross: ui.Center, Stroke: pink, Border: 1, Radius: 12,
			Help: "shell/theme#pick", Label: "Pick mode"}, func(c *ui.Ctx) {
			c.Text("l", "PICK MODE · CLICK ANYTHING", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .1}, Color: pink})
		}).Clicked {
			e.fail(e.startPick())
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("ms", fmt.Sprintf("repainted in %.1fms", float64(e.paintDur.Microseconds())/1000), monoAt(th, 11, theme.FgFaint))
	})
}

func (e *themeEditor) fixture(c *ui.Ctx, th *theme.Resolved) *fixture {
	if f, ok := e.fixtures[th]; ok {
		return f
	}
	clear(e.fixtures) // only the themes on screen now
	f := &fixture{canvas: scenes.NewCanvas(c.Shaper(), th)}
	e.fixtures[th] = f
	return f
}

// probeFixture draws the fixture in th's probe theme on the CPU, for a click in the preview.
func (e *themeEditor) probeFixture(sh typeset.Shaper, th *theme.Resolved) (*image.RGBA, *theme.Resolved) {
	p := theme.Probe(th)
	var l render.List
	cv := scenes.NewCanvas(sh, p)
	cv.Begin(l.Record(1))
	scenes.IDE(cv)
	return raster.Render(&l, render.Point{X: scenes.IDEWidth, Y: scenes.IDEHeight}, 1, p.C(theme.BgBase)), p
}

// noBackdrop keeps a scene drawn inside the window from changing the window's backdrop.
type noBackdrop struct{ render.Frame }

func (noBackdrop) Backdrop(render.Color) {}

// preview is the fixture workspace (render/scenes) in the theme, or split against the base. A click picks what
// painted the pixel.
func (e *themeEditor) preview(c *ui.Ctx, th *theme.Resolved) {
	themes := []*theme.Resolved{th}
	if e.tab == 1 {
		if b := e.baseTheme(th.Variant); b != nil {
			themes = append(themes, b)
		}
	}
	c.Row("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(16), Gap: 12, Fill: th.C(tThemeStage)}, func(c *ui.Ctx) {
		for i, t := range themes {
			f := e.fixture(c, t)
			id := c.Next(i)
			hit := c.Interaction(id)
			if hit.Clicked {
				if r, ok := c.Rect(id); ok {
					img, p := e.probeFixture(c.Shaper(), t)
					if name, _ := probeAt(img, p, image.Pt(int(hit.Pointer().X-r.Min.X), int(hit.Pointer().Y-r.Min.Y))); name != "" {
						e.choose(name)
					}
				}
			}
			now := c.Now
			c.Box(i, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Stroke: th.C(theme.LineStrong), Border: 1, Radius: 6, Clip: true, Interactive: true,
				Cursor: render.CursorCrosshair, Help: "shell/theme#preview", Role: ui.RoleRegion, Label: "Preview in " + t.Name}, func(c *ui.Ctx) {
				c.Paint("fixture", ui.Flex(1), ui.Flex(1), func(fr render.Frame, r render.Rect) {
					start := now()
					fr.PushClip(r, 6)
					f.canvas.F = render.Offset(noBackdrop{fr}, r.Min)
					scenes.IDE(f.canvas)
					fr.Pop()
					e.paintDur = now().Sub(start)
				})
			})
		}
	})
}

// baseTheme is the theme the current one extends, resolved in v.
func (e *themeEditor) baseTheme(v theme.Variant) *theme.Resolved {
	b := e.base()
	if b == "" {
		return nil
	}
	for t := range e.fixtures {
		if t.Name == b && t.Variant == v {
			return t
		}
	}
	srcs, _ := e.h.Themes()
	st, err := theme.Stack(srcs, b)
	if err != nil {
		return nil
	}
	r, err := theme.Resolve(st, v)
	if err != nil {
		return nil
	}
	return r
}

// strip is the theme file as it's written, the line last written lit.
func (e *themeEditor) strip(c *ui.Ctx, th *theme.Resolved) {
	path, src := e.file()
	lines := strings.Split(strings.TrimRight(string(src), "\n"), "\n")
	c.Row("strip", ui.Style{W: ui.Flex(1), H: ui.Px(132), Pad: ui.PadXY(14, 12), Gap: 24, Fill: th.C(tThemePanel)}, func(c *ui.Ctx) {
		c.Column("where", ui.Style{W: ui.Px(150), H: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
			c.Text("w", "WRITING TO", capLabel(th))
			shown := "a local theme, on your first edit"
			if path != "" {
				shown = filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path))
			}
			widget.Wrap(c, "path", shown, 150, monoAt(th, 11, theme.Fg))
			if path != "" {
				c.Text("n", fmt.Sprintf("%d overrides · %d lines", overrides(src), len(lines)), monoAt(th, 10, theme.FgFaint))
			}
		})
		c.Column("toml", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true, Help: "shell/theme#toml"}, func(c *ui.Ctx) {
			for i, l := range lines {
				e.tomlLine(c, th, i, l)
			}
		})
		c.Column("acts", ui.Style{Gap: 8, Main: ui.End, H: ui.Flex(1)}, func(c *ui.Ctx) {
			if widget.Button(c, "open", "Open file", widget.ButtonOpts{Disabled: path == "", Help: "shell/theme#toml"}) {
				e.open = false
				e.fail(e.h.Run(context.Background(), "editor.open", registry.Args{"path": path}))
			}
			if widget.Button(c, "undo", "Undo ⌘Z", widget.ButtonOpts{Disabled: len(e.undo) == 0, Help: "shell/theme#toml"}) {
				e.fail(e.undoLast())
			}
		})
	})
}

func (e *themeEditor) tomlLine(c *ui.Ctx, th *theme.Resolved, i int, l string) {
	key, val, isKV := strings.Cut(l, "=")
	t := strings.TrimSpace(l)
	lit := e.written != "" && isKV && strings.Trim(strings.TrimSpace(key), `"`) == strings.TrimPrefix(strings.TrimPrefix(e.written, "palette:"), "reset:")
	c.Row(i, ui.Style{H: ui.Px(17), Cross: ui.Center}, func(c *ui.Ctx) {
		switch {
		case strings.HasPrefix(t, "["):
			c.Text("h", l, monoAt(th, 11.5, tThemeTOMLHead))
		case isKV:
			kt := tThemeTOMLKey
			if lit {
				kt = tThemeWritten
			}
			c.Text("k", fmt.Sprintf("%-22s", strings.TrimSpace(key)), monoAt(th, 11.5, kt))
			c.Text("v", "= "+strings.TrimSpace(val), monoAt(th, 11.5, tThemeTOMLVal))
			if lit {
				c.Text("now", "    # ← just now", monoAt(th, 11.5, tThemeWritten))
			}
		default:
			c.Text("t", l, monoAt(th, 11.5, theme.FgFaint))
		}
	})
}

// file is the theme file edits go to, without making one: its path and contents, or nothing yet.
func (e *themeEditor) file() (string, []byte) {
	name := e.themeName()
	srcs, _ := e.h.Themes()
	for i := len(srcs) - 1; i >= 0; i-- {
		if srcs[i].Name() != name {
			continue
		}
		var path string
		switch s := srcs[i].(type) {
		case theme.File:
			path = s.Path
		case *liveFile:
			path = s.Path
		}
		if path == "" {
			break
		}
		b, _ := os.ReadFile(path)
		return path, b
	}
	return "", nil
}

// inspector is the right column: the chain, the value, contrast and lint.
func (e *themeEditor) inspector(c *ui.Ctx, th *theme.Resolved) {
	if e.loaded != e.sel {
		e.load(th)
	}
	c.Column("inspect", ui.Style{W: ui.Px(340), H: ui.Flex(1), Fill: th.C(tThemePanel)}, func(c *ui.Ctx) {
		c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true}, func(c *ui.Ctx) {
			e.chain(c, th)
			c.Box("r1", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			e.value(c, th)
			c.Box("r2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			e.contrast(c, th)
		})
		c.Box("r3", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("acts", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Cross: ui.Center}, func(c *ui.Ctx) {
			if widget.Button(c, "reset", "Reset to inherited", widget.ButtonOpts{Help: "shell/theme#toml"}) {
				e.fail(e.reset(e.sel))
			}
		})
	})
}

func (e *themeEditor) load(th *theme.Resolved) {
	e.loaded = e.sel
	e.expr = exprOf(th, e.sel)
	col := colorOf(th, e.sel)
	e.hex = theme.Hex(col)
	e.lch = theme.ToOKLCH(col)
	if math.IsNaN(e.lch.H) {
		e.lch.H = 0
	}
}

func kindOf(name string) string {
	return strings.ToUpper(theme.Section(name)[0])
}

func (e *themeEditor) chain(c *ui.Ctx, th *theme.Resolved) {
	links := th.Chain(e.picked)
	if len(links) == 0 {
		links = []theme.Link{{Name: e.picked, Expr: exprOf(th, e.picked)}}
	}
	c.Column("chain", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 8}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Text("t", "PICKED", capLabel(th))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", e.picked, monoAt(th, 10, tThemePick))
		})
		for i, l := range links {
			on := l.Name == e.sel
			stroke := th.C(theme.Line)
			if on {
				stroke = th.C(theme.Accent)
			}
			label := strings.TrimPrefix(l.Name, "palette:")
			in := widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(10, 0), Gap: 10, Cross: ui.Center, Stroke: stroke,
				Border: 1, Radius: 5, Fill: th.C(theme.BgBase), Help: "shell/theme#chain", Role: ui.RoleListItem, Label: label, Selected: on}, func(c *ui.Ctx) {
				c.Box("sw", ui.Style{W: ui.Px(14), H: ui.Px(14), Radius: 3, Fill: colorOf(th, l.Name), Stroke: th.C(theme.LineStrong), Border: 1}, nil)
				c.Text("n", label, monoAt(th, 11.5, theme.Fg))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				kind := kindOf(l.Name)
				if on {
					kind += " · EDIT"
				}
				c.Text("k", kind, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Tracking: .1}, Color: th.C(theme.FgGhost)})
			})
			if in.Clicked {
				e.sel = l.Name
			}
		}
		if n := e.dependants(th, e.sel); n > 0 && e.sel != e.picked {
			widget.Wrap(c, "note", fmt.Sprintf("Editing %s also changes %d other tokens. Pick the top link to change only %s.",
				strings.TrimPrefix(e.sel, "palette:"), n, e.picked), 310, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
		}
	})
}

// dependants counts the tokens whose chain runs through name, not counting name itself.
func (e *themeEditor) dependants(th *theme.Resolved, name string) int {
	n := 0
	for _, t := range theme.Tokens() {
		if t.Name == name {
			continue
		}
		if slices.ContainsFunc(th.Chain(t.Name), func(l theme.Link) bool { return l.Name == name }) {
			n++
		}
	}
	return n
}

func (e *themeEditor) value(c *ui.Ctx, th *theme.Resolved) {
	c.Column("value", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 10}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "VALUE", capLabel(th))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			widget.Segmented(c, "mode", []string{"DERIVE", "FIXED"}, &e.mode, widget.SegmentedOpts{Help: "shell/theme#value"})
		})
		col := colorOf(th, e.sel)
		if e.mode == 0 {
			c.Row("expr", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				if widget.Input(c, "in", &e.expr, widget.InputOpts{Label: "Expression", Help: "shell/theme#value"}).Submitted {
					e.fail(e.set(e.sel, e.expr))
				}
				c.Text("eq", "= "+theme.Hex(col), monoAt(th, 11, theme.FgFaint))
			})
			return
		}
		lch := e.lch
		track := func(f func(t float64) theme.OKLCH) func(float64) render.Color {
			return func(t float64) render.Color { return theme.FromOKLCH(f(t)) }
		}
		sliders := []struct {
			key, label string
			v          *float64
			max, step  float64
			at         func(t float64) theme.OKLCH
			fmt        string
		}{
			{"l", "L", &e.lch.L, 1, 0.005, func(t float64) theme.OKLCH { return theme.OKLCH{L: t, C: lch.C, H: lch.H, A: 1} }, "%.3f"},
			{"c", "C", &e.lch.C, 0.37, 0.002, func(t float64) theme.OKLCH { return theme.OKLCH{L: lch.L, C: t * .37, H: lch.H, A: 1} }, "%.3f"},
			{"h", "H", &e.lch.H, 360, 1, func(t float64) theme.OKLCH { return theme.OKLCH{L: lch.L, C: max(lch.C, .1), H: t * 360, A: 1} }, "%.0f°"},
		}
		changed := false
		for _, s := range sliders {
			c.Row(s.key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("l", s.label, monoAt(th, 10, theme.FgFaint))
				if widget.Slider(c, "s", s.v, widget.SliderOpts{Min: 0, Max: s.max, Step: s.step, Width: ui.Flex(1), Label: s.label,
					Track: track(s.at), Help: "shell/theme#value"}) {
					changed = true
				}
				c.Text("v", fmt.Sprintf(s.fmt, *s.v), monoAt(th, 11, theme.Fg))
			})
		}
		if changed {
			e.lch.A = 1
			e.hex = theme.Hex(theme.FromOKLCH(e.lch))
			e.fail(e.setting(e.sel, fmt.Sprintf("oklch(%.3f %.3f %.1f)", e.lch.L, e.lch.C, e.lch.H), true))
		}
		c.Row("hex", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("sw", ui.Style{W: ui.Px(24), H: ui.Px(24), Radius: 4, Fill: col, Stroke: th.C(theme.LineStrong), Border: 1}, nil)
			if widget.Input(c, "in", &e.hex, widget.InputOpts{Label: "Hex", Width: ui.Px(120), Help: "shell/theme#value"}).Submitted {
				e.fail(e.set(e.sel, e.hex))
				e.loaded = ""
			}
		})
	})
}

func (e *themeEditor) contrast(c *ui.Ctx, th *theme.Resolved) {
	if e.lintTh != th {
		e.lint, e.lintTh = theme.Lint(th), th
	}
	c.Column("contrast", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 10}, func(c *ui.Ctx) {
		c.Text("t", "CONTRAST", capLabel(th))
		var pairs []theme.Pair
		for _, p := range theme.Pairs() {
			if p.FG == e.sel || p.On == e.sel {
				pairs = append(pairs, p)
			}
		}
		if len(pairs) == 0 {
			c.Text("none", "No declared pairs for "+strings.TrimPrefix(e.sel, "palette:")+".", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
		}
		for row := 0; row*3 < len(pairs); row++ {
			c.Row(row, ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				for i := row * 3; i < min(len(pairs), row*3+3); i++ {
					p := pairs[i]
					fg, _ := th.Color(p.FG)
					bg, _ := th.Color(p.On)
					ratio := theme.ContrastRatio(fg, bg)
					tone := theme.OK
					if ratio < p.Min {
						tone = theme.Warn
					}
					other := p.On
					if p.On == e.sel {
						other = p.FG
					}
					c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.Pad(8), Gap: 4, Stroke: th.C(theme.Line), Border: 1, Radius: 5, Fill: bg,
						Role: ui.RoleText, Label: fmt.Sprintf("%s on %s: %.1f to 1", p.FG, p.On, ratio)}, func(c *ui.Ctx) {
						c.Text("o", short(other, 14), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5}, Color: fg})
						c.Text("r", fmt.Sprintf("%.1f:1", ratio), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 14, Weight: typeset.SemiBold}, Color: th.C(tone)})
					})
				}
			})
		}
		for i, f := range e.lint {
			c.Column(fmt.Sprint("lint", i), ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
				widget.Wrap(c, "m", "⚠ "+f.Message, 310, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgMuted)})
				if f.Fix != "" && widget.Link(c, "fix", "Fix: "+f.Token+" = "+f.Fix, "shell/theme#contrast") {
					e.fail(e.set(f.Token, f.Fix))
				}
			})
		}
	})
}

func (e *themeEditor) footer(c *ui.Ctx, th *theme.Resolved) {
	_, src := e.file()
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(26), Gap: 12, Cross: ui.Center, Fill: th.C(tThemePanel), Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
		c.Row("mode", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(12, 0), Cross: ui.Center, Fill: th.C(theme.Accent)}, func(c *ui.Ctx) {
			c.Text("t", "THEME", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.Bold, Tracking: .1}, Color: th.C(theme.AccentInk)})
		})
		c.Text("name", e.themeName(), monoAt(th, 11, theme.FgMuted))
		c.Text("n", fmt.Sprintf("● %d overrides", overrides(src)), monoAt(th, 10.5, theme.FgFaint))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("keys", "p pick · r reset · u undo · esc close", monoAt(th, 10.5, theme.FgFaint))
		c.Box("pad", ui.Style{W: ui.Px(4)}, nil)
	})
}

// overrides counts the values a theme file sets.
func overrides(src []byte) int {
	s, err := theme.ParseSpec(src)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range []theme.Layer{s.Layer, s.Dark, s.Light} {
		n += len(l.Palette) + len(l.Tokens) + len(l.Syntax) + len(l.Effects)
	}
	return n
}
