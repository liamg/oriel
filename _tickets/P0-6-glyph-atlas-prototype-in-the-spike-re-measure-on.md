---
id: P0-6
title: "Glyph-atlas prototype in the spike; re-measure on macOS; go/no-go"
phase: P0
status: todo
area: Spike and repo
module: -
depends_on: [P0-4]
---

# P0-6 · Glyph-atlas prototype in the spike; re-measure on macOS; go/no-go

**Phase** 0 · spike and repo · **After** [P0-4](P0-4-gio-spike-measure-the-budgets-re-create-01-ide-n.md) · **Unblocks** [SH-1](SH-1-window-gpu-rendering-hidpi-and-1-text.md)

## Why

On macOS the P0-4 spike missed budgets 1 (scroll p99 11.9 ms), 4 (HUD 7.5% CPU) and 6 (RSS 146–279 MB). The CPU side is
under 1.2 ms p99; the GPU time is Gio stenciling every glyph as a path (`spikes/P0-4-gio.md`).

## Do

- Text through a glyph atlas (go-text shaping, rasterised coverage, stem darkening) drawn as image quads, behind the
  spike's existing scenes.
- Profile RSS (heap vs fonts vs GPU buffers) and the 2.9% idle CPU.
- Re-run `-auto` and `-bench` on macOS against the P0-4 baseline.

## Done when

Budgets 1, 4 and 6 pass on macOS, or the report says why not and recommends the fallback. Results in `spikes/P0-4-gio.md`.

## Design

- `_design/shell.md` (budgets, way 1), `_design/rendering.md` (glyph atlas)
- `_design/spikes/P0-4-gio.md`
