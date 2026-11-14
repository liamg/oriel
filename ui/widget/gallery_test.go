package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// gallery is every widget in a typical state: the fixture for the widget snapshots.
type gallery struct {
	text      string
	sel, tab  int
	popOpen   bool
	modalOpen bool
	toasts    Toasts
	target    ui.ID
	anchor    ui.ID
	input     ui.ID
	list      ui.ID
}

func (g *gallery) build(c *ui.Ctx) {
	th := c.Theme()
	c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(20), Gap: 16, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
		c.Row("buttons", ui.Style{Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			Button(c, "primary", "Allow host", ButtonOpts{Primary: true, Help: "gallery/button"})
			Button(c, "plain", "Once", ButtonOpts{Help: "gallery/button"})
			Button(c, "ghost", "Ghost", ButtonOpts{Ghost: true, Help: "gallery/button"})
			Button(c, "off", "Disabled", ButtonOpts{Disabled: true, Help: "gallery/button"})
			Chip(c, "c1", "1 needs you", th.C(theme.StatusNeedsYou), "gallery/chip")
			Chip(c, "c2", "1 working", th.C(theme.StatusWorking), "gallery/chip")
			Chip(c, "c3", "gopls ✓", th.C(theme.Accent), "gallery/chip")
			g.anchor = c.Next("anchor")
			Button(c, "anchor", "Popover", ButtonOpts{Help: "gallery/button"})
		})
		Popover(c, "pop", g.anchor, &g.popOpen, PopoverOpts{Width: 220, Help: "gallery/popover"}, func(c *ui.Ctx) {
			c.Text("t", "A popover under its anchor.", ui.TextStyle{})
		})
		g.input = c.Next("input")
		Input(c, "input", &g.text, InputOpts{Label: "Ask", Placeholder: "Why did it jump 9% this morning?", Width: ui.Px(420), Help: "gallery/input"})
		Tabs(c, "tabs", []string{"AGENT · CLAUDE", "TERMINAL", "PROBLEMS 1", "DEBUG", "TESTS"}, &g.tab, TabsOpts{Help: "gallery/tabs"})
		c.Row("panes", ui.Style{Gap: 16, W: ui.Flex(1), H: ui.Px(200)}, func(c *ui.Ctx) {
			c.Column("tree", ui.Style{W: ui.Px(250), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
				Tree(c, "tree", []TreeItem{
					{ID: "internal", Label: "internal", Swatch: render.Color{R: 0x5c, G: 0xd2, B: 0xff, A: 255}, Children: []TreeItem{
						{ID: "round.go", Label: "round.go", Swatch: render.Color{R: 0x4f, G: 0xc1, B: 0xe9, A: 255}, Mark: "M", MarkTone: th.C(theme.Warn)},
						{ID: "tiers.go", Label: "tiers.go", Swatch: render.Color{R: 0x4f, G: 0xc1, B: 0xe9, A: 255}},
					}},
					{ID: "PLAN.md", Label: "PLAN.md", Swatch: render.Color{R: 0xff, G: 0xcf, B: 0x5c, A: 255}, Mark: "A", MarkTone: th.C(theme.OK)},
					{ID: "money.go", Label: "money.go", Note: "← amount.go", Glyph: "●", GlyphTone: th.C(theme.Warn), Mark: "R", MarkTone: th.C(theme.StatusPermission)},
					{ID: "legacy.go", Label: "legacy.go", Tone: th.C(theme.Error), Strike: true, Mark: "D", MarkTone: th.C(theme.Error)},
				}, TreeOpts{Help: "gallery/tree", Trailing: func(c *ui.Ctx, it TreeItem, _ bool) {
					if it.Mark != "" {
						DiffStat(c, "ds", 31, 9, DiffStatOpts{Bar: true})
					}
				}})
			})
			c.Column("list", ui.Style{W: ui.Px(250), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
				g.list = c.Next("list")
				List(c, "list", 500, &g.sel, ListOpts{Help: "gallery/list"}, func(c *ui.Ctx, i int, selected bool) {
					c.Row("r", ui.Style{Pad: ui.PadXY(10, 0), H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("t", "Negative values round the wrong way", ui.TextStyle{Color: th.C(theme.FgMuted)})
					})
				})
			})
			c.Column("menu", ui.Style{}, func(c *ui.Ctx) {
				Menu(c, "menu", items, MenuOpts{Help: "gallery/menu"})
			})
		})
		g.target = c.Box("target", ui.Style{W: ui.Px(120), H: ui.Px(24), Fill: th.C(theme.BgRaised), Radius: 4, Interactive: true, Help: "hud/usage#weekly-window"}, nil)
		ContextMenu(c, "ctx", g.target, items[:2])
	})
	Modal(c, "modal", &g.modalOpen, ModalOpts{Title: "Settings", Width: 360, Help: "gallery/modal"}, func(c *ui.Ctx) {
		c.Text("t", "A modal holds focus until it closes.", ui.TextStyle{})
		Button(c, "ok", "OK", ButtonOpts{Primary: true, Help: "gallery/button"})
	})
	ShowToasts(c, "toasts", &g.toasts)
}

var gallerySize = render.Point{X: 1000, Y: 640}

// setup drives the gallery into its states: a hovered button, a focused input with a selection, an expanded
// tree, a list and tabs moved along, the context menu and the popover open, and two toasts.
func setup(t *testing.T, v theme.Variant) (*harness, *gallery) {
	g := &gallery{text: "round half even", sel: 3, tab: 1}
	h := newHarness(t, v, gallerySize, g.build)
	h.frame()
	r := h.rect(g.input)
	h.click(r.Min.X+30, r.Min.Y+10)
	h.keys("End", "+Left", "+Left", "+Left", "+Left")
	x, y := mid(h.rect(g.target))
	h.rightClick(x, y)
	g.popOpen = true
	g.toasts.Push(Toast{Title: "git push origin rethink/rounding", Body: "gateway · 3 commits", Tone: render.Color{R: 0x4c, G: 0xc9, B: 0xf0, A: 255}, Help: "gallery/toast"})
	g.toasts.Push(Toast{Title: "CI passed #4417", Body: "github · 12 checks", Tone: render.Color{R: 0x4f, G: 0xe0, B: 0xa0, A: 255}, Help: "gallery/toast"})
	h.frame()
	h.frame()
	return h, g
}

func TestGallerySnapshots(t *testing.T) {
	for _, v := range []theme.Variant{theme.Dark, theme.Light} {
		h, _ := setup(t, v)
		snapshot.Golden(t, "testdata/gallery-"+string(v)+".list", []byte(snapshot.List(h.list)))
		img := raster.Render(h.list, gallerySize, 1, resolved(t, v).C(theme.BgBase))
		snapshot.GoldenPNG(t, "testdata/gallery-"+string(v)+".png", img, 0.02, 0.001)
	}
}

func TestModalSnapshots(t *testing.T) {
	for _, v := range []theme.Variant{theme.Dark, theme.Light} {
		g := &gallery{modalOpen: true}
		h := newHarness(t, v, gallerySize, g.build)
		h.frame()
		img := raster.Render(h.list, gallerySize, 1, resolved(t, v).C(theme.BgBase))
		snapshot.GoldenPNG(t, "testdata/modal-"+string(v)+".png", img, 0.02, 0.001)
	}
}

// Every interactive node in the gallery has a help id (the help lint).
func TestEveryControlHasHelp(t *testing.T) {
	h, g := setup(t, theme.Dark)
	g.modalOpen = true
	h.frame()
	h.frame()
	if missing := h.u.WithoutHelp(); len(missing) > 0 {
		t.Errorf("%d interactive nodes have no help id", len(missing))
	}
}
