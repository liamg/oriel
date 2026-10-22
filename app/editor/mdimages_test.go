package editor_test

import (
	"bytes"
	"image/color"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/registry"
)

// remotePictures is a client answering remote images from files instead of the network, each fetch held until
// release; an unknown URL is a 404.
func remotePictures(t *testing.T, files map[string][]byte) (release func(), client *http.Client) {
	gate := make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release, &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		select {
		case <-gate:
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
		b, ok := files[r.URL.String()]
		if !ok {
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: http.NoBody, Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(b)), Request: r}, nil
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A remote (`http(s)`) image is fetched in the background: the document draws at once, the image a placeholder with
// its alt text and loading dots until it's in, then the image; one that fails is a chip saying why.
func TestMarkdownRemoteImages(t *testing.T) {
	const (
		ci      = "https://github.com/liamg/oriel/actions/workflows/ci.yml/badge.svg"
		license = "https://img.shields.io/badge/license-MIT-blue.svg"
	)
	release, client := remotePictures(t, map[string][]byte{ci: editor.PNGBytes(t, editor.SolidImage(100, 20, color.NRGBA{G: 200, A: 255})), license: []byte(editor.Badge)})
	s := readmeScene(t, client)
	if !drew(s, "Oriel") || !drew(s, "A vim-first IDE") {
		t.Fatalf("the document waits for its images:\n%s", s.Text())
	}
	for _, name := range []string{"Loading CI", "Loading License"} {
		s.NodeRole("status", name)
	}
	release()
	framesUntil(s, t, "the badges to load", func(int) {}, func() bool {
		n := s.FindRole("image", "License")
		return n != nil && n.Bounds.Dx() == 90
	})
	if w := s.NodeRole("image", "CI").Bounds.Dx(); w != 100 {
		t.Errorf("the CI badge is %v wide, want the fetched PNG's 100", w)
	}
	if s.FindRole("status", "Loading License") != nil {
		t.Error("the loading dots stay after the badge is in")
	}
}

// readmeScene is a README previewed: a centred logo, a title, badges and a screenshot, with an image the preview finds
// beside it.
func readmeScene(t *testing.T, images *http.Client) *apptest.Scene {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	os.WriteFile(filepath.Join(dir, "logo.png"), editor.PNGBytes(t, photo(256, 256)), 0o644)
	os.WriteFile(filepath.Join(dir, "docs", "shot.png"), editor.PNGBytes(t, photo(1200, 700)), 0o644)
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
	s := threadsSceneWith(t, apptest.Options{WorkDir: dir, ImageNet: images})
	s.Run("editor.open", registry.Args{"path": "README.md"})
	s.Frames(3)
	s.Run("MarkdownPreview", nil)
	s.Frames(4)
	return s
}

// A block's images are drawn in rows that wrap at the column, at their `width`/`height`, never wider than it; each
// is zoomable, and a document's images are one lightbox group.
func TestMarkdownPreviewImages(t *testing.T) {
	s := readmeScene(t, nil)
	logo := s.NodeRole("image", "Oriel logo")
	if w := logo.Bounds.Dx(); w != 120 {
		t.Errorf("the logo is %v wide, want its width attribute's 120", w)
	}
	shot := s.NodeRole("image", "The editor and its threads")
	for _, want := range []string{"Oriel", "A vim-first IDE. Press", "⌘K", "CI", "License", "https://img.shields.io", "▸ Building from source", "make build"} {
		if !drew(s, want) {
			t.Errorf("the preview lacks %q:\n%s", want, s.Text())
		}
	}
	for _, not := range []string{"<p", "<img", "<kbd>", "<details>", "align="} {
		if drew(s, not) {
			t.Errorf("the preview shows %q:\n%s", not, s.Text())
		}
	}
	if c := (logo.Bounds.Min.X + logo.Bounds.Max.X) / 2; c < shot.Bounds.Min.X+shot.Bounds.Dx()/2-80 {
		t.Errorf("the centred logo is at %v, left of the column's middle", c)
	}
	s.Click(apptest.Centre(shot.Bounds), 0)
	s.Frames(2)
	if !drew(s, "2 / 2") || !drew(s, "1200×700") {
		t.Errorf("a click on the screenshot doesn't show the document's images in the lightbox:\n%s", s.Text())
	}
	s.Key("Left")
	s.Frames(2)
	if !drew(s, "1 / 2") || !drew(s, "256×256") {
		t.Errorf("← doesn't go to the logo:\n%s", s.Text())
	}
}
