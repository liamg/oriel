package widget

import (
	"image"
	"image/color"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// gradient is a w×h image whose red is its x and whose green is its y, each scaled to 0..255, so a pixel drawn says
// where in the image it came from.
func gradient(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / w), G: uint8(y * 255 / h), B: 0x80, A: 0xff})
		}
	}
	return img
}

var lightboxWindow = render.Point{X: 800, Y: 600}

// lightboxScene is two zoomable images of one group, and a third of its own, side by side.
func lightboxScene(t *testing.T, a, b image.Image) (*harness, *[3]ui.ID) {
	var ids [3]ui.ID
	h := newHarness(t, theme.Dark, lightboxWindow, func(c *ui.Ctx) {
		c.Row("row", ui.Style{Pad: ui.Pad(20), Gap: 20}, func(c *ui.Ctx) {
			ids[0] = Image(c, "a", a, 100, ImageOpts{Label: "the first", Help: "test/image", Zoom: true, Group: "doc"})
			ids[1] = Image(c, "b", b, 100, ImageOpts{Label: "the second", Help: "test/image", Zoom: true, Group: "doc"})
			ids[2] = Image(c, "c", b, 100, ImageOpts{Label: "alone", Help: "test/image", Zoom: true})
		})
	})
	return h, &ids
}

// dialog is the lightbox's node in the accessibility tree, or nil while it's closed.
func (h *harness) dialog() *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n == nil {
			return nil
		}
		if n.Role == ui.RoleDialog {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(h.u.AccessTree())
}

func (h *harness) images() []render.Op {
	var out []render.Op
	for _, op := range h.list.Ops {
		if op.Kind == render.OpImage {
			out = append(out, op)
		}
	}
	return out
}

func (h *harness) clickImage(id ui.ID) {
	h.t.Helper()
	x, y := mid(h.rect(id))
	h.click(x, y)
	h.frame()
	if h.dialog() == nil {
		h.t.Fatal("a click on a zoomable image opens no lightbox")
	}
}

// A click on an image shows it in the lightbox (`widget.Lightbox`, at `LayerDialog`): large over a
// scrim, fit to the window. Escape, a click on the scrim or on the fitted image, or its ✕ closes it; ← and → move
// between the images of its group, counted n / m under it with its caption.
func TestLightboxOpensAndCloses(t *testing.T) {
	h, ids := lightboxScene(t, gradient(400, 200), gradient(300, 300))
	h.clickImage(ids[0])
	if n := h.dialog(); n.Name != "the first" {
		t.Errorf("the lightbox is named %q", n.Name)
	}
	if !h.drew("the first") || !h.drew("1 / 2") {
		t.Errorf("caption and count: %q", h.texts())
	}
	// 400×200 fits the room the window leaves it at its own size, centred in it.
	var big bool
	for _, op := range h.images() {
		big = big || op.Image.Rect.Dx() == 400 && op.Image.Rect.Dy() == 200
	}
	if !big {
		t.Error("the image isn't drawn at its own size")
	}
	h.keys("Right")
	if !h.drew("the second") || !h.drew("2 / 2") {
		t.Errorf("→: %q", h.texts())
	}
	h.keys("Right")
	if !h.drew("2 / 2") {
		t.Errorf("→ past the last: %q", h.texts())
	}
	h.keys("Left")
	if !h.drew("1 / 2") {
		t.Errorf("←: %q", h.texts())
	}
	h.keys("Esc")
	if h.dialog() != nil {
		t.Fatal("Escape leaves it open")
	}

	h.clickImage(ids[0])
	h.click(10, 300) // the scrim, beside the image
	if h.dialog() != nil {
		t.Fatal("a click on the scrim leaves it open")
	}
	h.clickImage(ids[0])
	h.click(lightboxWindow.X/2, (lbTop+lightboxWindow.Y-lbBottom)/2) // the fitted image
	if h.dialog() != nil {
		t.Fatal("a click on the fitted image leaves it open")
	}
	h.clickImage(ids[0])
	h.click(lightboxWindow.X-16-17, 14+17) // ✕
	h.frame()
	if h.dialog() != nil {
		t.Fatal("✕ leaves it open")
	}

	h.clickImage(ids[2])
	if h.drew("1 / 1") || h.drew("1 / 2") || !h.drew("alone") {
		t.Errorf("an image of no group is shown alone: %q", h.texts())
	}
	h.keys("Right")
	if !h.drew("alone") {
		t.Errorf("→ on an image alone: %q", h.texts())
	}
}

// pixel is the colour drawn at p, in the window's pixels.
func (h *harness) pixel(p render.Point) color.RGBA {
	img := raster.Render(h.list, h.size, 1, color.NRGBA{A: 0xff})
	return img.RGBAAt(int(p.X), int(p.Y))
}

func near(a, b color.RGBA, tol int) bool {
	d := func(x, y uint8) int { return max(int(x)-int(y), int(y)-int(x)) }
	return d(a.R, b.R) <= tol && d(a.G, b.G) <= tol
}

func (h *harness) wheel(p render.Point, dy float32) {
	h.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	h.frame()
	h.u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: p, Scroll: render.Point{Y: dy}})
	h.frame()
}

// The wheel zooms around the pointer, a drag pans the zoomed image, a double-click or 0 fits
// it again and 1 shows it at its own size. Only the part in view is scaled, so zooming far into a large image costs
// no more than the window.
func TestLightboxZoomsAndPans(t *testing.T) {
	h, ids := lightboxScene(t, gradient(2000, 1000), gradient(10, 10))
	h.clickImage(ids[0])
	p := render.Point{X: 300, Y: 260}
	before := h.pixel(p)
	h.wheel(p, -240)
	if !h.drew("2000×1000 · 91%") {
		t.Errorf("zoomed in by e from the fit: %q", h.texts())
	}
	if after := h.pixel(p); !near(before, after, 2) {
		t.Errorf("the point under the pointer moved: %v, then %v", before, after)
	}
	if h.dialog() == nil {
		t.Fatal("the wheel closed it")
	}

	under := h.pixel(p)
	h.drag(p, render.Point{X: p.X - 100, Y: p.Y})
	if h.dialog() == nil {
		t.Fatal("a drag closed it")
	}
	if got := h.pixel(render.Point{X: p.X - 100, Y: p.Y}); !near(under, got, 2) {
		t.Errorf("dragged 100 left: %v there, want %v", got, under)
	}

	x, y := p.X, p.Y
	h.click(x, y)
	if h.dialog() == nil {
		t.Fatal("a click on the zoomed image closed it")
	}
	h.click(x, y)
	if !h.drew("2000×1000 · 34%") {
		t.Errorf("a double-click fits it again: %q", h.texts())
	}

	h.keys("1")
	if !h.drew("2000×1000 · 100%") {
		t.Errorf("1: %q", h.texts())
	}
	h.keys("0")
	if !h.drew("2000×1000 · 34%") {
		t.Errorf("0: %q", h.texts())
	}

	for range 12 {
		h.wheel(p, -480)
	}
	if !h.drew("2000×1000 · 3200%") {
		t.Errorf("zoomed in as far as it goes: %q", h.texts())
	}
	for _, op := range h.images() {
		if r := op.Image.Rect; r.Dx() > int(lightboxWindow.X)+1 || r.Dy() > int(lightboxWindow.Y)+1 {
			t.Errorf("an image %v drawn bigger than the window", r)
		}
	}
	if !slices.ContainsFunc(h.images(), func(op render.Op) bool { return op.At.X <= 0 && op.At.Y <= 0 }) {
		t.Error("the zoomed image doesn't cover the window")
	}
}

// With `ImageOpts.Zoom` an image takes the pointer cursor and an edge while hovered.
func TestZoomableImageHover(t *testing.T) {
	h, ids := lightboxScene(t, gradient(400, 200), gradient(300, 300))
	x, y := mid(h.rect(ids[0]))
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	if c := h.u.Cursor(); c != render.CursorPointer {
		t.Errorf("cursor %v over a zoomable image", c)
	}
	edge := false
	for _, op := range h.list.Ops {
		edge = edge || op.Kind == render.OpRect && op.Style.Width > 0 && op.Rect == h.rect(ids[0])
	}
	if !edge {
		t.Error("no edge on the hovered image")
	}
}

// Opened from a page, the lightbox takes the keys: Escape closes it and leaves the page up.
func TestLightboxOverAPage(t *testing.T) {
	open := true
	var id ui.ID
	h := newHarness(t, theme.Dark, lightboxWindow, func(c *ui.Ctx) {
		Page(c, "page", &open, PageOpts{KeyMode: "page", Help: "test/page"}, func(c *ui.Ctx) {
			id = Image(c, "a", gradient(300, 200), 120, ImageOpts{Label: "a chart", Help: "test/image", Zoom: true})
		})
	})
	h.frame()
	h.clickImage(id)
	h.frame()
	h.keys("Esc")
	if h.dialog() != nil && h.dialog().Name == "a chart" {
		t.Fatal("Escape leaves the lightbox up")
	}
	if !open {
		t.Fatal("Escape at the lightbox closed the page under it")
	}
}

// A vector (a diagram) is zoomable like an image, in its document's group, and draws itself at
// the size the lightbox shows it, so it stays sharp however far it's zoomed.
func TestLightboxVector(t *testing.T) {
	var drawn []render.Rect
	v := Vector{Size: render.Point{X: 200, Y: 100}, Fill: color.NRGBA{R: 0x20, G: 0x20, B: 0x20, A: 0xff},
		Draw: func(f render.Frame, r render.Rect) {
			drawn = append(drawn, r)
			f.Rect(r, render.Style{Fill: color.NRGBA{R: 0xff, A: 0xff}})
		}}
	var ids [3]ui.ID
	h := newHarness(t, theme.Dark, lightboxWindow, func(c *ui.Ctx) {
		c.Row("row", ui.Style{Pad: ui.Pad(20), Gap: 20}, func(c *ui.Ctx) {
			ids[0] = Image(c, "a", gradient(40, 40), 100, ImageOpts{Label: "the first", Zoom: true, Group: "doc"})
			ids[1] = VectorImage(c, "v", v, 120, ImageOpts{Label: "the diagram", Zoom: true, Group: "doc"})
			ids[2] = Image(c, "b", gradient(40, 40), 100, ImageOpts{Label: "the last", Zoom: true, Group: "doc"})
		})
	})
	if r := h.rect(ids[1]); r.Dx() != 120 || r.Dy() != 60 {
		t.Errorf("the vector is drawn at %v, want 120×60", r)
	}
	if !slices.Contains(drawn, h.rect(ids[1])) {
		t.Errorf("the vector draws into %v, not its box %v", drawn, h.rect(ids[1]))
	}
	h.clickImage(ids[1])
	if n := h.dialog(); n.Name != "the diagram" || !h.drew("2 / 3") {
		t.Errorf("the lightbox shows %q: %q", n.Name, h.texts())
	}
	// 200×100 fits the room at twice its size: a vector isn't held to its own size as pixels are.
	drawn = nil
	h.frame()
	if !h.drew("200×100 · 200%") || len(drawn) == 0 || drawn[len(drawn)-1].Dx() != 400 {
		t.Errorf("fitted: %q, drawn into %v", h.texts(), drawn)
	}
	p := render.Point{X: 400, Y: 300}
	h.wheel(p, -240)
	drawn = nil
	h.frame()
	if len(drawn) == 0 || drawn[len(drawn)-1].Dx() <= 400 {
		t.Errorf("zoomed in, it's drawn into %v", drawn)
	}
	h.keys("Left")
	if !h.drew("the first") || !h.drew("1 / 3") {
		t.Errorf("← from the vector: %q", h.texts())
	}
	h.keys("Right", "Right")
	if !h.drew("the last") {
		t.Errorf("→ past the vector: %q", h.texts())
	}
}

// A vector's tips are tooltips over its parts, drawn at whatever size it's shown: text a diagram cut short shows in
// full under the pointer, inline and in the lightbox.
func TestVectorTips(t *testing.T) {
	v := Vector{Size: render.Point{X: 200, Y: 100}, Draw: func(f render.Frame, r render.Rect) {},
		Tips: []VectorTip{{Rect: render.XYWH(100, 0, 100, 50), Text: "the whole label"}}}
	for _, zoom := range []bool{false, true} {
		var id ui.ID
		h := newHarness(t, theme.Dark, lightboxWindow, func(c *ui.Ctx) {
			c.Box("at", ui.Style{Pad: ui.Pad(20)}, func(c *ui.Ctx) {
				id = VectorImage(c, "v", v, 100, ImageOpts{Label: "a diagram", Zoom: zoom, Help: "test/image"})
			})
		})
		rest := func(x, y float32) {
			h.pointer(render.PointerMove, 0, x, y)
			h.frame()
			h.clock.Advance(time.Second)
			h.frame()
			h.frame()
		}
		r := h.rect(id) // 100×50: the tip is its top right quarter
		rest(r.Min.X+75, r.Min.Y+10)
		if !h.drew("the whole label") {
			t.Errorf("zoom %v: no tooltip over the tip: %q", zoom, h.texts())
		}
		rest(r.Min.X+25, r.Min.Y+40)
		if h.drew("the whole label") {
			t.Errorf("zoom %v: the tooltip shows away from its part", zoom)
		}
		if !zoom {
			continue
		}
		h.clickImage(id)
		// fitted at twice its size, 400×200 centred in the room: the tip is its top right quarter
		cx, cy := lightboxWindow.X/2, float32(lbTop+(lightboxWindow.Y-lbBottom-lbTop)/2)
		rest(cx+100, cy-50)
		if !h.drew("the whole label") {
			t.Errorf("no tooltip over the tip in the lightbox: %q", h.texts())
		}
	}
}
