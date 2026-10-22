package editor_test

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/vim"
)

// photo is a w×h picture: a sky over a field with a sun, so a screenshot of it reads as one.
func photo(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.NRGBA{R: uint8(60 + 80*y/h), G: uint8(120 + 60*y/h), B: 230, A: 255}
			if y > h*2/3 {
				c = color.NRGBA{R: 50, G: uint8(140 + 40*x/w), B: 60, A: 255}
			}
			if dx, dy := x-w*3/4, y-h/4; dx*dx+dy*dy < (h/8)*(h/8) {
				c = color.NRGBA{R: 255, G: 210, B: 70, A: 255}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

// A file a `registry.FileViewer` claims opens in that viewer's view instead of a buffer. The built-in `image` viewer
// shows an image centred and fit to the tab, with its dimensions, size and format under it, zoomable, and reloads it
// when the file changes.
func TestImageFileTab(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "assets", "hero.png")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, editor.PNGBytes(t, photo(640, 400)), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 800}})
	s.Now = apptest.At(9, 0, 0)
	s.Run("editor.open", registry.Args{"path": "assets/hero.png", "line": 3})
	s.Frames(3)
	if !s.Showing(editor.ImageView) {
		t.Fatalf("hero.png didn't open in the image view:\n%s", s.Text())
	}
	for _, want := range []string{"hero.png", "640 × 400 · ", "· PNG"} {
		if !drew(s, want) {
			t.Errorf("the tab lacks %q:\n%s", want, s.Text())
		}
	}
	if slices.ContainsFunc(engine(s).WindowIDs(), func(id vim.WindowID) bool { p, _ := engine(s).Pane(id); return p.Name() == "assets/hero.png" }) {
		t.Error("hero.png was read into a buffer too")
	}
	img := s.NodeRole("image", "hero.png")
	s.Click(apptest.Centre(img.Bounds), 0)
	s.Frames(2)
	if !drew(s, "640×400 · 100%") {
		t.Errorf("a click on the image doesn't show it in the lightbox:\n%s", s.Text())
	}
	if !s.Key("Esc") {
		t.Fatal("the lightbox doesn't take Escape")
	}
	s.Frames(2)
	if drew(s, "640×400 · 100%") {
		t.Error("Escape leaves the lightbox up")
	}

	// Opened again, it's the same tab; changed on disk, it's read again.
	s.Run("editor.open", registry.Args{"path": p})
	os.WriteFile(p, editor.PNGBytes(t, photo(320, 200)), 0o644)
	framesUntil(s, t, "the tab to follow the file", func(int) {}, func() bool { return drew(s, "320 × 200 · ") })
	if n := strings.Count(s.Text(), "hero.png"); n > 2 {
		t.Errorf("hero.png is open %d times", n)
	}
}

// A video, audio, a PDF or another binary file is a card with its name, type and size and "Open in default app"
// (`open` on macOS, `xdg-open` on Linux).
func TestMediaFileCard(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "demo.mp4"), make([]byte, 3<<20), 0o644)
	os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("ab\x00cd"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 800}})
	s.Run("editor.open", registry.Args{"path": "demo.mp4"})
	s.Frames(3)
	for _, want := range []string{"demo.mp4", "MP4 video · 3.0 MB", "Oriel doesn't play video.", "Open in default app"} {
		if !drew(s, want) {
			t.Errorf("the card lacks %q:\n%s", want, s.Text())
		}
	}
	s.Click(s.TextPoint("Open in default app"), 0)
	s.Frames(1)
	if want := filepath.Join(dir, "demo.mp4"); !slices.Contains(s.Desktop.Opened, want) {
		t.Errorf("opened %q, want %s", s.Desktop.Opened, want)
	}
	s.Run("editor.open", registry.Args{"path": "blob.bin"})
	s.Frames(2)
	if !drew(s, "binary file · 5 B") {
		t.Errorf("a binary file's card:\n%s", s.Text())
	}
	// Text still opens as text.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0o644)
	s.Run("editor.open", registry.Args{"path": "notes.txt"})
	s.Frames(2)
	if !s.Showing(kit.EditorView) || !drew(s, "hello") {
		t.Errorf("a text file didn't open in the editor:\n%s", s.Text())
	}
}

// An SVG is drawn the same way, as vectors; one that can't be read is a card saying why.
func TestSVGFileTab(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(editor.Badge), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.svg"), []byte("<svg width="), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 800}})
	s.Run("editor.open", registry.Args{"path": "logo.svg"})
	s.Frames(3)
	if !s.Showing(editor.ImageView) {
		t.Fatalf("logo.svg didn't open in the image view:\n%s", s.Text())
	}
	for _, want := range []string{"90 × 20 · ", "· SVG"} {
		if !drew(s, want) {
			t.Errorf("the tab lacks %q:\n%s", want, s.Text())
		}
	}
	img := s.NodeRole("image", "logo.svg")
	if w := img.Bounds.Dx(); w != 90 {
		t.Errorf("the SVG is %v wide, want its own 90", w)
	}
	s.Click(apptest.Centre(img.Bounds), 0)
	s.Frames(2)
	if !drew(s, "90×20") {
		t.Errorf("a click on the SVG doesn't show it in the lightbox:\n%s", s.Text())
	}
	s.Key("Esc")
	s.Frames(2)
	s.Run("editor.open", registry.Args{"path": "broken.svg"})
	s.Frames(2)
	if !drew(s, "SVG image") || !drew(s, "It can't be read as an image") {
		t.Errorf("a broken SVG's card:\n%s", s.Text())
	}
}
