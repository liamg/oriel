# P0-4 · Gio spike: go/no-go

**Verdict so far: provisional GO on Gio.** It reproduces `01-ide` closely, with no limit that rules out a mockup.
The budget numbers that decide it (frame time, keypress-to-photon, 1× text, idle RSS, Wayland scaling) have to come
from real GPUs on macOS and Linux. This sandbox only has software GL. **Status: review** until the macOS run below is in.

Code: `cmd/oriel-spike` (its own module, build tag `spike`, so the rest of the workspace builds without Gio's
cgo headers). `scene/` holds the drawing in pure Go; `main` holds the window, headless, benchmark and compare
modes. Gio `v0.10.3`.

![01-ide drawn by Gio, 1×](P0-4-gio/ide-1x.png)

## What was done here (Linux sandbox, no GPU)

- Debian 12 container, i9-9880H (16 threads), no root, no display. Gio's Linux dev libraries and Mesa were
  extracted from the bookworm `.deb`s into a scratch prefix. **All GPU numbers below are from llvmpipe (Mesa's
  software rasteriser) over surfaceless EGL**: valid for correctness and for the CPU cost of building a frame,
  meaningless for GPU frame time.
- Xvfb couldn't start without root (its `xkbcomp` path is compiled in), so the **windowed** path (`-auto`) is
  built and vetted but was **not run** here. The new sandbox image (`.crit/sandbox.Dockerfile`) has Xvfb and Mesa
  installed properly, so it can be run there.
- `GOOS=darwin` can't be cross-built: `gioui.org/app` needs cgo against the macOS SDK. The `scene` package builds
  for darwin; the binary must be built on the Mac.

### 01-ide, side by side

`-compare` against `mockups/01-ide.png`, both 1600×1000 @1×: **mean absolute difference 7.2/255; 5.5% of pixels
differ by more than 32/255.** What makes up that 5.5%:

- Missing glyphs: the rail icons (`⌥ ⌕ ⎇ ⚙ ◈`), `◐` and the braille spinner `⠼`. The sandbox has no symbol
  fonts. Gio falls back to system fonts (go-text `fontscan`), so on macOS they come from Apple Symbols. Not a Gio limit.
- Code text runs about 0.1 px per character wider: Chrome rounds JetBrains Mono's 8.1 px advance to 8 px at 1×,
  and Gio keeps the fractional advance. That's about 5 px over a 50-column line.
- Ligatures in the terminal pane: `===` renders as a ligature because Gio's shaper can't turn features off
  (see the gaps below). The mockup uses `font-variant-ligatures: none` there.
- Everything else matches: layout, colours, chips, corner brackets, the thread card, squiggles, avatar
  gradients, glows, the titlebar's accent hairline.

![mockup | Gio | diff](P0-4-gio/ide-mockup-native-diff.png)

Code at 13.5 px, 1×, zoomed 2× with nearest-neighbour. The mockup (Chrome/Skia, FreeType) is on top, Gio below:

![mockup](P0-4-gio/code-mockup-1x-zoom2.png)
![gio](P0-4-gio/code-gio-1x-zoom2.png)

At 1× Gio's unhinted, greyscale-AA text is about as sharp as Chrome's on Linux, and slightly heavier.
Renders at 1.25×, 1.5× and 2× (`-shot … -scales 1,1.25,1.5,2`) are crisp, because text is shaped at device
pixels, not scaled.

The text sampler (`-scene text`) covers ligatures, italics, weights, hairlines, code and UI sizes, and light on dark next to dark on light:

![text sampler](P0-4-gio/text-1x.png)

### Numbers measured here

`oriel-spike -bench`: 300 frames per scene at 1600×1000 @1×, headless.

| Scene | Build ops (CPU) p50 / p99 | llvmpipe draw p50 / p99 |
|---|---|---|
| `ide` (01-ide) | 0.55 / 0.78 ms | 36.9 / 40.8 ms |
| `scroll` (50k-line Go file, cursor +1 line per frame, diagnostics and virtual text) | 0.50 / 0.85 ms | 30.4 / 47.0 ms |
| `text` (sampler) | 0.28 / 0.38 ms | 17.3 / 18.9 ms |
| `glow` (ide + 50 pulsing 9-slice glows + opacity-layer modal) | 0.63 / 0.94 ms | 44.4 / 47.7 ms |
| `hud` (30 live panes) | 0.29 / 1.33 ms | 34.0 / 38.4 ms |
| `type` (empty buffer) | 0.03 / 0.04 ms | 2.5 / 3.1 ms |

- **Building a frame is cheap**: under 1.4 ms p99 for every scene, including shaping. The p99 tail is the first
  frame of a scene filling the shaper's caches.
- A CPU profile of the whole run: **87% of CPU time is inside cgo (llvmpipe rasterising)**. Gio's own Go
  renderer (op collection, path encoding, stencil setup) is roughly 2–3 ms a frame on top of building the ops.
  Most of the GPU work is `stencilClips`: **Gio redraws every glyph as a vector path, through a stencil, every
  frame**. On a real GPU that's cheap, but it is the cost the glyph atlas (`rendering.md`, way 1) removes.
- Startup: **~270 ms** from process start to the first `ide` frame drawn, headless. That includes parsing the
  bundled fonts, scanning system fonts and starting llvmpipe.
- RSS: **195 MB** peak after the first `ide` frame, and 423 MB after every scene. Both include llvmpipe's buffers,
  and the second includes the 50k-line file and its token cache. Not comparable with the 120 MB budget; the
  macOS run measures it.

## Budgets: status

| # | Check (`shell.md`) | Status |
|---|---|---|
| 1 | Hold `j` in a 50k-line file: frame time p99 < 8 ms at 120 Hz | **macOS run needed.** CPU side measured here: build p99 0.85 ms |
| 2 | Keypress-to-photon < 8 ms (typometer) | **macOS run needed.** `-scene type` is the typometer target; `-auto` also prints the in-app part |
| 3 | Ligatures, italics, emoji, CJK, 0.5 px hairline | Ligatures, italics and hairlines ✓ here. Emoji and CJK use system fallback: **check on macOS** (the sandbox has no such fonts) |
| 3 | 13 px code at 1× vs a FreeType-hinted terminal | Close to Chrome/FreeType here. **Check by eye on the external 1× monitor** |
| 3 | 9-slice glow on 50 elements at 120 Hz; opacity modal over a live editor | Both drawn (`-scene glow`). Build p99 0.94 ms. **GPU time: macOS run** |
| 4 | 30-pane HUD at 4 Hz < 3% CPU | **macOS run needed** (`-auto` measures it) |
| 5 | Wayland fractional scaling 1.25× and 1.5×, no blur | Headless renders at 1.25× and 1.5× are crisp. **A real Wayland session is still needed** (not available here) |
| 6 | Idle RSS < 120 MB | **macOS run needed** (`-auto` ends with 5 s idle on `ide`) |

## For Liam to run on macOS

Needs Xcode's command-line tools (for cgo). From the repo root:

```sh
go run -tags spike ./cmd/oriel-spike -auto                        # windowed: the scripted run, prints every number above
go run -tags spike ./cmd/oriel-spike -bench                       # headless, Metal: build vs GPU time per scene
go run -tags spike ./cmd/oriel-spike -shot /tmp/spike -scales 1,2 # PNGs of every scene
go run -tags spike ./cmd/oriel-spike -scene type                  # then point typometer at it
go run -tags spike ./cmd/oriel-spike -scene text                  # drag to the 1× monitor and compare with a terminal
```

`-auto` opens a 1600×1000 window and runs:

1. 120 frames of `ide`.
2. 1200 frames of `scroll`, moving the cursor down one line every frame (held `j` at 120 Hz).
3. 600 frames of `glow`.
4. 60 frames of `text`.
5. 10 s of `hud` redrawing at 4 Hz.
6. 5 s idle.

It prints:

- startup to the first presented frame
- build, `Frame()` and frame-to-frame interval percentiles for each continuous phase (an interval p99 over 8.3 ms means dropped frames at 120 Hz)
- CPU % for the HUD and idle phases
- the in-app part of key latency
- peak RSS

Paste the output into this doc. On Linux, run the same commands in a Wayland session at 1.25× and 1.5×.

## What the spike found (gaps and what we do about them)

| Finding | Impact | Plan |
|---|---|---|
| **Gio blends in linear light; the mockups (CSS) blend in sRGB.** A 7% green row tint came out 3× too bright (`21,66,45` vs `11,21,16`). | Every translucent token (`accentBg`, diff rows, chips, scrims, glows, opacity layers) looks far stronger than designed. | The spike remaps alpha so blending over dark surfaces matches sRGB (`Canvas.SRGBBlend`, `srgbAlpha`). Then the same pixel is `8,21,14`. For real: `theme` resolves translucent component tokens to **opaque colours over their known surface**, and `render/gio` keeps the remap for overlays over unknown backdrops. Added to `rendering.md`. |
| **Dark text on light looks thin** (linear-light blending of glyph coverage). | The light theme (`17-light`) will look weak. | The glyph atlas with stem darkening and gamma (`rendering.md`) is needed before the light theme ships, not just for 1× sharpness. |
| **No letter-spacing** in Gio's text API. | Every cap micro-label (`.14em`) and the brand. | Drawn glyph by glyph in the spike; `typeset` owns tracking. |
| **No font-feature control** in Gio's shaper (go-text supports it; Gio doesn't pass it through). | Terminals and anything that sets `ligatures: none` or `ss01…`. | `typeset` shapes with go-text directly and hands glyphs or outlines to the backend. |
| **Glyphs are vector paths stenciled every frame.** | Most of the GPU time per frame; fine on a GPU, a ceiling for big scroll scenes. | Glyph atlas (way 1 in `shell.md`), measured against this baseline. |
| **No blur.** Glows are baked Gaussian 9-slices (`scene/glow.go`). They look right, and are cached per size, blur and colour, so animate glows with an opacity layer, not by changing their colour. | As designed in `shell.md`. | Keep; move into `render/gio` as `Effect(glow)`. |
| `gioui.org/app` pulls in the Go fonts and material icons (for fallback window decorations), and IBus over D-Bus on X11. | 32 MB `vendor/`, and `godbus` (approved). | Accept. |

Nothing found rules out a mockup. Glows, the opacity modal, hairlines, gradients, rounded and stroked shapes,
clipping, and fractional scaling all work through Gio's public API.

## Recommendation

- **Go**, pending the macOS numbers: if 1, 2 and 6 pass there, SH-1 starts on Gio.
- Build `typeset` (go-text shaping with features and tracking, then a glyph atlas with stem darkening) early in
  phase 1. It fixes three of the findings above.
- Theme tokens resolve to opaque colours where the surface is known (`theming.md` already derives with `mix()`).
