# Rendering and UI toolkit

How we get beautiful text and a UI we can bend into anything.

## Layers

```mermaid
flowchart TB
  App["app: screens, panels, HUD"] --> UI["ui: widgets, layout, theme, motion"]
  UI --> R["render: Surface, Path, Text, Image, Effect"]
  R --> Gio["render/gio"]
  R --> Wgpu["render/wgpu (later)"]
  UI --> Text["typeset: shaping, fallback, atlas"]
  Text --> R
```

## `render` interface

- Retained display lists, built per frame, with damage regions (the dirty
  rects we actually redraw).
- Primitives: rect (radius, border, gradient), path, glyph run, image, clip,
  transform, and **effects**: blur, glow, scanline, noise. The sci-fi look
  comes from a handful of cheap shader effects, never from 3D.
- Implemented first on Gio's `op` lists. Effects Gio lacks (blur, glow) are
  drawn with prebaked textures until we own a renderer.

## Text (`typeset`)

- Shaping: `go-text/typesetting` (a HarfBuzz port): ligatures, kerning, `calt`, `ss01..ss20`.
- Fonts: bundled JetBrains Mono, Inter and Space Grotesk, plus system fallback via fontconfig and CoreText.
- Rasterise to a **glyph atlas**, keyed by (font, size, subpixel x-offset in quarters).
  Grayscale AA by default; subpixel LCD AA when the backend allows.
- Monospace fast path: shape a whole line once, cache it by content hash, and
  reuse it while the line is unchanged.
- Proportional text (plan docs, threads, tours) goes through the same pipeline,
  with paragraph layout and line breaking (UAX #14).
- Gamma-correct blending and stem darkening for light-on-dark text. This is
  most of what makes the difference between "terminal" and "beautiful".
- Letter-spacing (tracking) and font features (`liga`, `calt`, `ss01`…) are `typeset`'s job: Gio's text API
  has neither, so `typeset` shapes with go-text directly (P0-4).

## Blending

- Gio blends in **linear light**; CSS, and so the mockups, blend in sRGB. A translucent token that looks right in
  a mockup comes out much stronger in Gio (a 7% tint about 3× as bright; `spikes/P0-4-gio.md`).
- So `theme` resolves translucent component tokens to **opaque colours over the surface they sit on**, and
  `render/gio` remaps alpha for overlays whose backdrop isn't known (scrims, glows, opacity layers).
- Dark text on light looks thin under linear blending, so the glyph atlas's gamma and stem darkening must land
  before the light theme.

## `ui` toolkit

- **Immediate-mode API, retained layout cache.** Widgets are functions:
  `Panel(ctx, id, func(ctx){ … })`. State is keyed by id. No widget objects, no DOM.
- Layout: flex rows and columns plus absolute overlays and anchors. An
  "anchor" widget can pin to a buffer position, which is how callouts and
  explain notes follow code.
- **Theme** is data (TOML), with layered tokens, overrides only and a live editor. See `theming.md`.
- **Motion**: a spring-based animator per id. Used sparingly: status pulses,
  panel slides, number roll-ups.
- **Input**: a keymap layer above widgets: vim modes, then app chords
  (`⌘K`, `⌘1..9`), then the focused widget. Everything is rebindable.
- **Accessibility**: an AccessKit-style tree emitted from the same widget ids,
  so screen readers aren't an afterthought. This matters if we open-source it.

## Performance budgets

| Thing | Budget |
|---|---|
| keypress → photon | < 8 ms |
| frame (scroll, 50k-line file) | < 4 ms CPU, 120 Hz |
| idle CPU, HUD live | < 1% |
| cold start to editable buffer | < 300 ms |
| RSS with 6 sessions open | < 400 MB (excluding LSPs) |

- Every PR touching `render`, `typeset` or `ui` runs a benchmark scene and
  fails CI on a regression of more than 10%.

## Go interfaces

```go
// package render — what a backend (Gio now, wgpu later) implements
type Backend interface {
    NewWindow(o WindowOpts) (Window, error)
}

type Window interface {
    Frame(fn func(f Frame)) error                 // called on vsync when damaged
    Events() <-chan Event                         // keys, IME, pointer, resize, scale, focus
    Scale() float32
}

type Frame interface {
    Rect(r Rect, s Style)                         // fill, border, radius, gradient
    Path(p *Path, s Style)
    Glyphs(run *typeset.Run, at Point, c Color)
    Image(img ImageID, dst Rect)
    Effect(kind EffectKind, r Rect, amt float32)  // glow, blur, grid, scanline
    Push(clip Rect, t Transform)
    Pop()
    Damage() []Rect
}
```

```go
// package typeset — shaping and the atlas, independent of the backend
type Shaper interface {
    Shape(text []byte, f FontSpec, feats Features) *Run // cached by (text, font, features)
}

type Atlas interface {
    Glyph(font FontID, gid GlyphID, size float32, subpx uint8) (TexRegion, error)
}
```

```go
// package ui — immediate-mode widgets are just functions over a Ctx
type Ctx interface {
    ID(key any) Ctx                               // stable identity for state and animation
    Theme() *theme.Resolved
    Frame() render.Frame
    Input() *InputState
    Anim(target float32) float32                  // spring towards target, keyed by ID
}

func Tat(c ui.Ctx, label string, frac float32, tone theme.Token) // a HUD readout
func Panel(c ui.Ctx, title string, body func(ui.Ctx))
```

```go
// package layout — one tree for every view; docks are its edges
type Placement int // Docked | Split | Tab | Float | DropDown

type Node interface{ isNode() }
type Split struct{ Dir Axis; Children []Node; Weights []float32 }
type Leaf  struct{ Tabs []ViewRef; Active int }
type Root  struct{ Main Node; Docks map[Edge]*Leaf; Floats []*Float; Zoomed *ViewRef }

type Layout interface {
    Open(v ViewRef, p Placement, opts Opts) error
    Move(v ViewRef, p Placement, opts Opts) error // ⌃W M, drag and drop
    Zoom(v ViewRef)                                // toggle; restores the saved tree
    Focus(dir Dir)                                 // ⌃W h/j/k/l across main and docks
    Save(name string) Snapshot
    Restore(s Snapshot)
}
```

