---
id: files/image
covers: [image]
related: [files/explorer, files/diff]
---

# Images and other files

A file that isn't text opens in a tab of its own instead of the editor. An image (PNG, JPEG, GIF, WebP, BMP or ICO)
is drawn centred and fit to the tab, with its width × height, file size and format under it. It's read again when the
file changes on disk. An animated GIF or WebP plays, here and wherever markdown shows it, and says how many frames it
has; with `ui.reduced_motion` on it stays on its first frame.

Click an image, here or in a markdown preview, a thread or the diff, to see it large in the lightbox: scroll
to zoom around the pointer, drag to move about, double-click or press `0` to fit it again and `1` for its own size,
`←` and `→` for the other images of the same document, and `Esc`, a click outside it or ✕ to close it.

## Other files {#card}

SVG images, video, audio, PDFs and other binary files show a card with their name, kind and size. **Open in default
app** opens the file with the app your system opens it with (`system.open`).

Plugins add viewers of their own for the files they claim (`oriel.editor.viewer`).
