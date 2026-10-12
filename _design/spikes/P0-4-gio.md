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

Measured on Liam's Mac (go1.26.5 darwin/arm64, 120 Hz, 1600×1000 @1×), 2026-10-12. Raw output below.

| # | Check (`shell.md`) | Result | Status |
|---|---|---|---|
| 1 | Hold `j` in a 50k-line file: frame p99 < 8 ms at 120 Hz | interval p99 11.87 ms (p50 8.33); build p99 1.13 ms, `Frame()` p99 5.38 ms; headless GPU draw p99 11.67 ms | **fail**: GPU-bound, frames dropped |
| 2 | Keypress-to-photon < 8 ms | in-app part 3.65 ms (n=1); typometer not run yet | **open** |
| 3 | Glow on 50 elements at 120 Hz | interval p99 16.75 ms; GPU draw p99 12.6 ms | **fail** (same cause as 1) |
| 3 | Emoji, CJK, 13 px at 1× by eye | not reported | **open** |
| 4 | 30-pane HUD at 4 Hz < 3% CPU | 7.46% | **fail** |
| 5 | Wayland 1.25× / 1.5× | not run | **open** |
| 6 | Idle RSS < 120 MB | 146 MB after the first frame (headless), 212 MB after every scene, 279 MB peak windowed | **fail** |
| – | Startup | 648 ms to first presented frame (windowed), 221 ms headless | no budget |
| – | Idle CPU | 2.9% with 2 frames in 5 s | should be ~0: investigate |

**Reading.** The CPU side is well inside budget everywhere (build p99 ≤ 1.15 ms). The GPU side is not: 5–7 ms p50 and
~12 ms p99 for scenes that are mostly text, which is Gio stenciling every glyph as a vector path each frame. That is the
cost the glyph atlas removes, so 1, 3 and probably 4 hinge on it. Memory is partly fixed overhead we chose (the Go
fonts, glyph path caches) and needs a profile before it's judged.

Per `shell.md`, a failure of 1 or 2 gets one focused round of renderer work before falling back. Next: **P0-6**, a time-boxed
glyph-atlas prototype in the spike, then re-run the same commands.

<details><summary>Raw macOS output</summary>

```
startup: first frame presented 648ms after start
ide     build ops n=120 p50=1.52ms p99=2.36ms max=2.40ms
        frame()   n=120 p50=2.67ms p99=6.65ms max=6.74ms
        interval  n=120 p50=8.33ms p99=16.84ms max=574.37ms
scroll  build ops n=1200 p50=0.59ms p99=1.13ms max=4.92ms
        frame()   n=1200 p50=2.58ms p99=5.38ms max=26.84ms
        interval  n=1200 p50=8.33ms p99=11.87ms max=3029.46ms
glow    build ops n=600 p50=2.71ms p99=3.63ms max=4.32ms
        frame()   n=600 p50=3.67ms p99=9.59ms max=10.26ms
        interval  n=600 p50=8.36ms p99=16.75ms max=29.20ms
text    build ops n=60 p50=0.33ms p99=0.68ms max=0.77ms
        frame()   n=60 p50=1.27ms p99=3.03ms max=3.68ms
        interval  n=60 p50=8.33ms p99=8.51ms max=18.44ms
hud     redrawing at 4 Hz over 10.033s: CPU 7.46% of one core (62 frames)
ide     idle over 5.025s: CPU 2.90% of one core (2 frames)
key event → frame presented (in-app part only): n=1 p50=3.65ms p99=3.65ms max=3.65ms
peak RSS: 279 MB
startup: first ide frame drawn 221ms after start (headless)
RSS after the first ide frame: 146 MB (peak so far)
go1.26.5 darwin/arm64, 300 frames per scene at 1600×1000 @1×
ide     build ops: n=300 p50=0.49ms p99=1.00ms max=1.16ms
        gpu draw:  n=300 p50=5.26ms p99=11.86ms max=13.41ms
scroll  build ops: n=300 p50=0.42ms p99=0.75ms max=3.97ms
        gpu draw:  n=300 p50=5.30ms p99=11.67ms max=12.69ms
text    build ops: n=300 p50=0.25ms p99=0.47ms max=7.57ms
        gpu draw:  n=300 p50=3.04ms p99=10.98ms max=11.53ms
glow    build ops: n=300 p50=0.52ms p99=1.15ms max=3.08ms
        gpu draw:  n=300 p50=7.20ms p99=12.60ms max=15.89ms
hud     build ops: n=300 p50=0.23ms p99=0.92ms max=1.62ms
        gpu draw:  n=300 p50=5.97ms p99=12.17ms max=13.05ms
type    build ops: n=300 p50=0.04ms p99=0.09ms max=0.23ms
        gpu draw:  n=300 p50=2.27ms p99=9.60ms max=10.15ms
peak RSS after every scene: 212 MB
```

</details>

## How to measure (macOS)

Run everything from the repo root. Close other heavy apps first, and keep the laptop on power.

### 0 · Once

1. Xcode command-line tools, needed for cgo: `xcode-select --install`.
2. Java, for typometer: `brew install openjdk`. This is Homebrew's own build: keg-only, no sudo, nothing installed system-wide.
   Run Java as `$(brew --prefix openjdk)/bin/java`.
3. Typometer: download and unzip it.
   ```sh
   curl -LO https://github.com/pavelfatin/typometer/releases/download/v1.0.1/typometer-1.0.1-bin.zip
   unzip typometer-1.0.1-bin.zip -d ~/typometer
   ```
4. Build the spike once so later runs don't include compile time: `go build -tags spike -o /tmp/oriel-spike ./cmd/oriel-spike`.

### 1 · Frame time, CPU and memory (budgets 1, 3, 4, 6)

```sh
/tmp/oriel-spike -auto     # opens a 1600×1000 window and runs every scene; ~30 s. Don't touch it.
/tmp/oriel-spike -bench    # headless (Metal): build time vs GPU time per scene
```

Paste both outputs into the thread. Run on the built-in Retina display. To also get 1× numbers, drag the `-auto` window to
the external 1× monitor as soon as it opens.

### 2 · Keypress-to-photon with typometer (budget 2)

1. Start the target: `/tmp/oriel-spike -scene type`. It shows a solid background, a "TYPE HERE" label and an empty line
   with no caret (typometer mistakes a caret for a block cursor and refuses to run).
2. Start typometer (it predates Java's module system, so it needs `--add-opens`): `$(brew --prefix openjdk)/bin/java --add-opens java.desktop/java.awt=ALL-UNNAMED -jar ~/typometer/typometer-1.0.1/typometer-1.0.1.jar` (the exact jar name may differ; use the
   `.jar` in the unzipped folder).
3. macOS asks for two permissions for the app that launched Java (your terminal): **Accessibility**, so it can type,
   and **Screen Recording**, so it can read pixels. Grant them in System Settings → Privacy & Security, then restart
   typometer. Those grants cover everything run from that terminal, so **revoke them when you're done**.
   Typometer is an unsigned jar from an individual (Pavel Fatin); if that's not acceptable, use the camera method below.
4. In typometer, click **Benchmark**. When it prompts you, click into the spike window within the countdown. It types
   `.....` to calibrate, then a run of dots, and measures each one. Leave the keyboard and mouse alone until it finishes.
   Clicking back on typometer stops it.
5. Run it again with a reference editor for comparison: a terminal running `nvim`, or Zed. Typometer goes through Java's
   AWT robot on macOS, which adds its own overhead, so the comparison matters more than the absolute number.
6. Paste the summary (min, avg, max, SD), or export the CSV, into the thread.

No-software alternative: film the keyboard and the screen together with an iPhone in 240 fps slow-mo, press a key in
`-scene type` about 20 times, and count frames from key-down to the dot appearing (each frame ≈ 4.2 ms). Do the same
with nvim in a terminal for comparison.

### 3 · Text quality by eye (budget 3)

1. `/tmp/oriel-spike -scene text`, then drag the window to the external 1× monitor.
2. Next to it, open a terminal (Terminal.app or Ghostty) showing the same code at 13 px in JetBrains Mono.
3. Compare sharpness, weight and spacing, plus emoji and CJK in the fallback rows. A screenshot of both side by side is
   enough. Repeat on the Retina display.

### 4 · Screenshots (optional)

`/tmp/oriel-spike -shot /tmp/spike -scales 1,2` writes PNGs of every scene. Compare `ide` with
`_design/mockups/01-ide.png`.

### 5 · Linux (budget 5)

The same commands in a **Wayland** session, with display scaling at 1.25× and then 1.5×, checking that text isn't blurry.
They need the libraries listed in `.crit/sandbox.Dockerfile` (`pkg-config libwayland-dev libxkbcommon-dev
libegl1-mesa-dev …`).

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

- **Not yet a go.** On macOS, 1, 4 and 6 fail, and all of them trace to glyph rendering or memory we haven't profiled.
  Do P0-6 (glyph atlas in the spike), re-measure, then decide. If 1 still fails, fall back per `shell.md`.
- Build `typeset` (go-text shaping with features and tracking, then a glyph atlas with stem darkening) early in
  phase 1. It fixes three of the findings above.
- Theme tokens resolve to opaque colours where the surface is known (`theming.md` already derives with `mix()`).
