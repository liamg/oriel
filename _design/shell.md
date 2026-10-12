# App shell: what we build the UI with

The choice that decides everything else: what draws the pixels.

## What we need from it

- Text that looks better than a terminal: real shaping, ligatures, subpixel AA.
- 120 fps scroll on a 50k-line file with diagnostics, inlays and inline threads.
- Under 8 ms from keypress to glyph on screen. Vim users notice anything slower.
- Freedom to draw anything: HUD overlays, glows, sparklines, annotated callouts.
- A core in Go, with the UI as a client of it.
- Runs on **macOS and Linux (Wayland + X11) from day one**. macOS is the primary machine. Windows can come later.
- Plugins can add UI without us shipping a browser engine for them.
- Open-sourceable: no licence traps.

## Candidates

| # | Option | UI lang | Renderer | Text quality | Perf ceiling | Go fit | Freedom | Risk |
|---|---|---|---|---|---|---|---|---|
| A | **Electron** + Go sidecar | TS | Chromium | great | good (VS Code) | sidecar | total | heavy RAM, JS UI |
| B | **Wails v3** | TS | system webview | varies by OS | **poor on Linux** (WebKitGTK) | native | total | WebKitGTK |
| C | **Tauri v2** + Go sidecar | TS | system webview | varies | same as B | sidecar | total | Rust + WebKitGTK |
| D | **Gio** (pure Go GPU) | Go | own GPU (Vulkan/Metal/GL) | good (HarfBuzz port) | high | **native** | high | thin widget set |
| E | **Custom Go renderer** (GLFW/SDL + wgpu/GL + atlas) | Go | ours | as good as we make it | highest | native | total | most work |
| F | **GPUI** (Zed's framework) | Rust | Metal/Vulkan | excellent | highest | FFI only | high | Rust, Zed-shaped API |
| G | **Flutter** + Go via FFI | Dart | Skia/Impeller | great | high | FFI | high | third language |
| H | **Fyne** | Go | GL | OK | medium | native | low | widget-kit look, slow text |
| I | **Qt/QML** | C++/QML | Qt Quick | great | high | dead bindings | medium | licensing, bindings |
| J | **Electron + WebGPU editor surface** | TS | Chromium + our GPU text | great | high | sidecar | total | two renderers |
| K | **Ultralight / CEF-lite** | TS | lightweight WebKit | good | good | cgo | high | proprietary licence |
| L | **Terminal UI** (Bubble Tea) | Go | terminal | terminal's | high | native | **low** | no HUD, no images |

Ruled out:

- **B, C**: on Linux the webview is WebKitGTK. Scrolling, compositing and font
  rendering there are the worst of the lot, and you live on Linux.
- **H, I, K, L**: the look, the licence, or the ceiling.
- **G**: a third language for no gain over D/E.
- **F**: best in class, but it pulls the UI into Rust, and you asked for Go.

## The real shortlist: A/J vs D/E

**Electron (A, plus J for the editor)**
- Fastest road to an expressive UI; plugins can ship HTML.
- VS Code shows the ceiling is fine, but it costs 400 MB+ idle and keystroke
  latency is in the 15–30 ms range.
- The editor ends up on a canvas or WebGPU surface anyway, so we write a text
  renderer either way.
- The UI is TypeScript, and the Go core sits behind IPC.

**Go-native GPU (D, then E where it bites)**
- One language from the vim engine to the pixels; no IPC on the hot path.
- Gio already has GPU paths, a HarfBuzz-grade shaper (`go-text/typesetting`),
  IME and Wayland. We put our own widget layer on it.
- Immediate mode suits a HUD that redraws live telemetry: no DOM diffing.
- Where Gio limits us (glyph atlas control, blur, subpixel AA), we swap in our
  own renderer behind the same `render` interface (option E).
- Cost: we build our own widgets. An agent writing them against a tight spec
  is where your free Claude Max goes furthest.

## Recommendation

**D → E: Go-native, starting on Gio, behind our own `render` and `ui` interfaces.**

- `ui` is our widget, layout and theme layer. It never imports Gio directly.
- `render/gio` is the first backend; `render/wgpu` comes later if we outgrow it.
- Plugin UI is declarative (a view tree over RPC; see `plugins.md`), so
  plugins never depend on the backend.
- The core is a headless server, so remote viewing (a phone, a teammate) can be built later **as a plugin**, without
  touching the desktop app. There's no built-in web client.

## Gio's renderer: what it can and can't draw

**Yes, it's GPU-rendered.** Gio records immediate-mode op lists each frame and draws them on the GPU
(OpenGL/ES, Vulkan, Direct3D 11 or Metal, depending on platform). Vector paths are rasterised on the GPU,
with a CPU fallback for paths where the GPU path isn't available.

**What's comfortably in reach** (most of the UI):
- Arbitrary filled and stroked paths, clips, affine transforms, rounded rects, hairlines.
- Solid colours, **linear** gradients, images and textures, opacity layers.
- Shaped text via go-text: ligatures, bidi, font fallback.
- Immediate-mode redraws at 120 Hz with only damaged frames redrawn.

**The limits** (to verify in the spike):

| Limit | Impact on us | Workaround |
|---|---|---|
| **No custom shaders** in the public API | glow, blur, scanlines and noise (the "effects" in `rendering.md`) | prebaked textures: a 9-slice glow sprite per radius and colour, cached and tinted. Cheap, and good enough for glows |
| **No blur / backdrop filter** | frosted scrims behind modals | a plain dark scrim (the mockups barely need blur); or our own offscreen pass (below) |
| **No radial or conic gradients, no blend modes** beyond normal over | some sci-fi flourishes | textures, or avoid them: the design language barely uses them |
| **Blending is in linear light**, not sRGB like CSS (found in P0-4) | translucent colours look much stronger than in the mockups; dark-on-light text looks thin | opaque theme tokens over known surfaces, an alpha remap for the rest (`rendering.md` → Blending) |
| **No letter-spacing or font-feature control** in the text API (found in P0-4) | cap labels, and ligatures off in terminals | `typeset` shapes with go-text directly |
| **Glyphs are drawn as vector outlines, unhinted, with greyscale AA** | text at small sizes on **1× (non-HiDPI) screens** looks softer than FreeType-hinted text. On HiDPI it's fine | the biggest risk to "beautiful text". Options: our own glyph atlas (hinted, optionally subpixel LCD) uploaded as images through Gio; or own the text pass |

**Ways past the limits, in order of cost:**
1. **Work within it**: textures for effects, our own glyph atlas for code text. That stays inside Gio's public API.
2. **Our own GPU pass alongside Gio**: Gio can render into a GPU context we own, so we draw effects and text
   with our own shaders and composite with Gio's output. Same window, same input.
3. **Contribute shader or effect support upstream** (or carry a small fork). Gio is small enough to read.
4. **Option E**: our own renderer behind `render.Backend`, keeping Gio only for windows, input and IME, or dropping it.
   The seam in `rendering.md` exists for exactly this.

None of these limits rules out anything in the mockups. Glows, blur and hinted small text are the three places to prove.

## Spike before committing (2 weeks)

**Gio is approved** (`deps.md`), on condition that the spike shows it can match the mockups in `ui.md` and finds no
limit that rules them out. Exit criterion 0: render `01-ide` and `06-review` natively, side by side with the PNGs.
**Results: `spikes/P0-4-gio.md`** (01-ide done; the GPU budgets are still to be measured on macOS).


Build a throwaway `oriel-spike` binary on Gio and measure it **on macOS (primary, Metal, Retina plus an external 1× monitor) and on Linux (Wayland)**:

1. Open a 50k-line Go file and hold `j`: frame time p99 under 8 ms at 120 Hz.
2. Keypress-to-photon latency under 8 ms (measured with `typometer`).
3. Ligatures, italics, emoji fallback, CJK, and a 0.5 px hairline that stays crisp.
   **Code text at 13 px on a 1× monitor next to the same text in a FreeType-hinted terminal**: if it's visibly softer,
   prototype the glyph-atlas path (way 1 above) before deciding.
   Also a 9-slice glow on 50 elements at 120 Hz, and an opacity-layer modal over a live editor.
4. A 30-pane HUD redrawing at 4 Hz uses under 3% CPU.
5. Wayland fractional scaling at 1.25× and 1.5× with no blurry text.
6. Idle RSS under 120 MB.

If 1, 2 or 5 fails and can't be fixed in a week of renderer work, fall back to
**A+J**. The core, the vim engine and the AI layers all survive that switch,
because none of them import the UI.

## Go interfaces

The swap point between shells is one interface, `render.Backend` (in `rendering.md`).

```go
func main() {
    var be render.Backend = gio.New()             // or wgpu.New(), or a test backend that records frames
    app.Run(be, cfg)
}
```

- A **headless test backend** records frames as display lists. UI tests and theme screenshots run without a GPU.
- If the spike forces Electron, the seam moves up a level. The Go core would expose the same `core`
  and `registry` over RPC, and a TypeScript UI would implement views. That's why plugin UI is a
  view tree and not Go draw calls.
