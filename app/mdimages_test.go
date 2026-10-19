package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

const readmeMD = `<p align="center">
  <img src="logo.png" alt="Logo" width="120">
</p>

<h1 align="center">Oriel</h1>

Badges: [![CI](https://example.com/ci.svg)](https://example.com) ![Shot](shot.png "a title")

Press <kbd>Ctrl</kbd>+<b>S</b> to save.<br>Done &amp; dusted, ` + "`<div>`" + ` kept.

<!-- a comment

over lines -->

<details>
<summary>More</summary>

Hidden body.

</details>

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="dark.png">
  <img src="light.png" alt="Diagram" width="50%">
</picture>

<sub>small print</sub> and <span class="x">unknown</span> tags.
`

// editor.md, "Images": "`![alt](src)` (inside a link too) and the HTML GitHub renders in a README. A line starting
// with a block tag […] or a comment starts an HTML block, up to a blank line: its headings, paragraphs and list items
// become blocks, `align=\"center\"` or `\"right\"` aligns them and their images, `<img src alt width height>` is an
// image (a `<picture>`'s `<img>` stands for its `<source>`s), `<br>` breaks the paragraph, and a `<summary>` is a bold
// \"▸\" line over its body. Inline, `<b>`/`<strong>` are bold, `<code>`/`<kbd>` code, `<a>`, `<sub>`, `<sup>`, `<i>`
// and tags it doesn't know are their text, and comments are hidden. Entities are decoded."
func TestMarkdownImagesAndHTML(t *testing.T) {
	type got struct {
		Kind   mdKind
		Level  int
		Text   string
		Align  string
		Images []mdImage
	}
	var bs []got
	for _, b := range mdParse(readmeMD) {
		bs = append(bs, got{b.Kind, b.Level, b.Text, b.Align, b.Images})
	}
	want := []got{
		{Kind: mdPara, Align: "center", Images: []mdImage{{Src: "logo.png", Alt: "Logo", W: 120}}},
		{Kind: mdHeading, Level: 1, Text: "Oriel", Align: "center"},
		{Kind: mdPara, Text: "Badges:", Images: []mdImage{{Src: "https://example.com/ci.svg", Alt: "CI"}, {Src: "shot.png", Alt: "Shot"}}},
		{Kind: mdPara, Text: "Press `Ctrl`+**S** to save. Done & dusted, `<div>` kept."},
		{Kind: mdPara, Text: "**▸ More**"},
		{Kind: mdPara, Text: "Hidden body."},
		{Kind: mdPara, Images: []mdImage{{Src: "light.png", Alt: "Diagram", WPct: .5}}},
		{Kind: mdPara, Text: "small print and unknown tags."},
	}
	if !reflect.DeepEqual(bs, want) {
		t.Errorf("blocks:\n%+v\nwant\n%+v", bs, want)
	}
	for _, b := range bs {
		if strings.Contains(b.Text, "comment") || strings.Contains(b.Text, "<") && !strings.Contains(b.Text, "`<div>`") {
			t.Errorf("a block shows HTML: %q", b.Text)
		}
	}
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func solidImage(w, h int, c color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// editor.md, "Images": "A relative `src` is found from the markdown file's folder (a thread's: its project's), and a
// `data:` URI is decoded. Oriel fetches nothing: a remote (`http(s)`) image, an SVG (nothing here rasterizes one) or a
// missing file is a chip with its alt text and source, its tooltip saying why."
func TestMarkdownPictureSources(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "a b.png"), pngBytes(t, solidImage(30, 20, color.NRGBA{R: 255, A: 255})), 0o644)
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte("<svg/>"), 0o644)
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, solidImage(4, 3, color.NRGBA{G: 255, A: 255})))
	for _, c := range []struct {
		src, dir string
		w        int
		why      string
	}{
		{"docs/a%20b.png", dir, 30, ""},
		{"./docs/a b.png?raw=true", dir, 30, ""},
		{data, "", 4, ""},
		{"https://example.com/x.png", dir, 0, "Remote images aren't fetched"},
		{"logo.svg", dir, 0, errSVG.Error()},
		{"docs/a b.png", "", 0, "There's no folder to find docs/a b.png in"},
	} {
		img, why := mdPicture(c.src, c.dir)
		switch {
		case c.w > 0 && (img == nil || img.Bounds().Dx() != c.w):
			t.Errorf("%.40s: %v (%s), want %d wide", c.src, img, why, c.w)
		case c.w == 0 && (img != nil || why != c.why):
			t.Errorf("%.40s: why %q, want %q", c.src, why, c.why)
		}
	}
	missing := filepath.Join(dir, "nope.png")
	if _, why := mdPicture(missing, ""); why == "" {
		t.Error("a missing file has no reason")
	}
}

// readmeScene is a README previewed: a centred logo, a title, badges and a screenshot, with an image the preview finds
// beside it.
func readmeScene(t *testing.T) *errScene {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "logo.png"), pngBytes(t, photo(256, 256)), 0o644)
	os.WriteFile(filepath.Join(dir, "docs", "shot.png"), pngBytes(t, photo(1200, 700)), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte(`<p align="center">
  <img src="logo.png" alt="Oriel logo" width="120">
</p>

<h1 align="center">Oriel</h1>

<p align="center">A vim-first IDE. Press <kbd>⌘K</kbd> to start.</p>

[![CI](https://github.com/liamg/oriel/actions/workflows/ci.yml/badge.svg)](https://github.com/liamg/oriel/actions) ![License](https://img.shields.io/badge/license-MIT-blue.svg)

## Screenshot

![The editor and its threads](docs/shot.png)

<details>
<summary>Building from source</summary>

Run <code>make build</code>.

</details>
`), 0o644)
	s := threadsScene(t, dir)
	s.run("editor.open", registry.Args{"path": "README.md"})
	s.frames(3)
	s.run("MarkdownPreview", nil)
	s.frames(4)
	return s
}

// editor.md, "Images": "A block's images are drawn in rows that wrap at the column, at their `width`/`height` […],
// never wider than it; each is zoomable, and a document's images are one lightbox group."
func TestMarkdownPreviewImages(t *testing.T) {
	s := readmeScene(t)
	logo := s.accessRole(t, "image", "Oriel logo")
	if w := logo.Bounds.Dx(); w != 120 {
		t.Errorf("the logo is %v wide, want its width attribute's 120", w)
	}
	shot := s.accessRole(t, "image", "The editor and its threads")
	for _, want := range []string{"Oriel", "A vim-first IDE. Press", "⌘K", "CI", "License", "https://img.shields.io", "▸ Building from source", "make build"} {
		if !s.drew(want) {
			t.Errorf("the preview lacks %q:\n%s", want, s.frameText())
		}
	}
	for _, not := range []string{"<p", "<img", "<kbd>", "<details>", "align="} {
		if s.drew(not) {
			t.Errorf("the preview shows %q:\n%s", not, s.frameText())
		}
	}
	if c := (logo.Bounds.Min.X + logo.Bounds.Max.X) / 2; c < shot.Bounds.Min.X+shot.Bounds.Dx()/2-80 {
		t.Errorf("the centred logo is at %v, left of the column's middle", c)
	}
	s.click(centre(shot.Bounds))
	s.frames(2)
	if !s.drew("2 / 2") || !s.drew("1200×700") {
		t.Errorf("a click on the screenshot doesn't show the document's images in the lightbox:\n%s", s.frameText())
	}
	s.Key("Left")
	s.frames(2)
	if !s.drew("1 / 2") || !s.drew("256×256") {
		t.Errorf("← doesn't go to the logo:\n%s", s.frameText())
	}
}
