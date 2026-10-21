package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

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
// `data:` URI is decoded. A remote (`http(s)`) image is fetched in the background […]. An SVG is drawn as vectors
// […]. A missing file or one that won't decode is a chip with its alt text and source, its tooltip saying why."
func TestMarkdownPictureSources(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "a b.png"), pngBytes(t, solidImage(30, 20, color.NRGBA{R: 255, A: 255})), 0o644)
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(testBadge), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.svg"), []byte("<svg width="), 0o644)
	data := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes(t, solidImage(4, 3, color.NRGBA{G: 255, A: 255})))
	svgData := "data:image/svg+xml;utf8," + url.PathEscape(testBadge)
	for _, c := range []struct {
		src, dir string
		w        float32
		why      string
	}{
		{"docs/a%20b.png", dir, 30, ""},
		{"./docs/a b.png?raw=true", dir, 30, ""},
		{data, "", 4, ""},
		{"logo.svg", dir, 90, ""},
		{svgData, "", 90, ""},
		{"docs/a b.png", "", 0, "There's no folder to find docs/a b.png in"},
	} {
		pc, why := mdPicture(c.src, c.dir)
		var w float32
		switch {
		case pc.img != nil:
			w = float32(pc.img.Bounds().Dx())
		case pc.doc != nil:
			w = pc.doc.Size.X
		}
		if w != c.w || why != c.why {
			t.Errorf("%.40s: %v wide, why %q; want %v, %q", c.src, w, why, c.w, c.why)
		}
	}
	for _, src := range []string{filepath.Join(dir, "nope.png"), "broken.svg"} {
		if pc, why := mdPicture(src, dir); why == "" || pc.img != nil || pc.doc != nil {
			t.Errorf("%s has no reason", src)
		}
	}
}

// testBadge is a 90×20 badge in shields.io's flat style.
const testBadge = `<svg xmlns="http://www.w3.org/2000/svg" width="90" height="20"><linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient><clipPath id="r"><rect width="90" height="20" rx="3" fill="#fff"/></clipPath><g clip-path="url(#r)"><rect width="51" height="20" fill="#555"/><rect x="51" width="39" height="20" fill="#007ec6"/><rect width="90" height="20" fill="url(#s)"/></g><g fill="#fff" text-anchor="middle" font-family="Verdana,sans-serif" font-size="110"><text x="265" y="140" transform="scale(.1)" textLength="410">license</text><text x="695" y="140" transform="scale(.1)" textLength="290">MIT</text></g></svg>`

// remotePictures answers remote images from files instead of the network, each fetch held until release; an unknown
// URL is a 404. It starts with nothing fetched.
func remotePictures(t *testing.T, files map[string][]byte) (release func()) {
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	old := fetchPicture
	fetchPicture = func(ctx context.Context, src string) ([]byte, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if b, ok := files[src]; ok {
			return b, nil
		}
		return nil, errors.New("the server answered 404 Not Found")
	}
	forgetRemote := func() {
		pictures.mu.Lock()
		defer pictures.mu.Unlock()
		for k := range pictures.m {
			if strings.HasPrefix(k, "remote:") {
				delete(pictures.m, k)
			}
		}
	}
	forgetRemote()
	t.Cleanup(func() { release(); fetchPicture = old; forgetRemote() })
	return release
}

// editor.md, "Images": "A remote (`http(s)`) image is fetched in the background: the document draws at once, the image
// a placeholder with its alt text and loading dots until it's in, then the image; one that fails is a chip saying why."
func TestMarkdownRemoteImages(t *testing.T) {
	const (
		ci      = "https://github.com/liamg/oriel/actions/workflows/ci.yml/badge.svg"
		license = "https://img.shields.io/badge/license-MIT-blue.svg"
	)
	release := remotePictures(t, map[string][]byte{ci: pngBytes(t, solidImage(100, 20, color.NRGBA{G: 200, A: 255})), license: []byte(testBadge)})
	s := readmeScene(t)
	if !s.drew("Oriel") || !s.drew("A vim-first IDE") {
		t.Fatalf("the document waits for its images:\n%s", s.frameText())
	}
	for _, name := range []string{"Loading CI", "Loading License"} {
		s.accessRole(t, "status", name)
	}
	release()
	s.framesUntil(t, "the badges to load", func(int) {}, func() bool {
		n := s.findRole("image", "License")
		return n != nil && n.Bounds.Dx() == 90
	})
	if w := s.accessRole(t, "image", "CI").Bounds.Dx(); w != 100 {
		t.Errorf("the CI badge is %v wide, want the fetched PNG's 100", w)
	}
	if s.findRole("status", "Loading License") != nil {
		t.Error("the loading dots stay after the badge is in")
	}
}

// A remote image that can't be fetched is a chip whose tooltip says why, and the fetch isn't tried again each frame.
func TestMarkdownRemoteImageFails(t *testing.T) {
	release := remotePictures(t, nil)
	release()
	pc, _ := mdPicture("https://example.com/gone.png", "")
	for i := 0; pc.loading && i < 500; i++ {
		time.Sleep(time.Millisecond)
		pc, _ = mdPicture("https://example.com/gone.png", "")
	}
	if pc, why := mdPicture("https://example.com/gone.png", ""); pc.loading || !strings.Contains(why, "404") {
		t.Errorf("a failed fetch: loading %v, why %q", pc.loading, why)
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
