package app

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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

// accessRole is the node with role and label in the accessibility tree.
func (s *errScene) accessRole(t *testing.T, role render.Role, label string) *render.AccessNode {
	t.Helper()
	n := s.findRole(role, label)
	if n == nil {
		t.Fatalf("no %s labelled %q", role, label)
	}
	return n
}

// findRole is the first node with role and label in the access tree, or nil.
func (s *errScene) findRole(role render.Role, label string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == role && n.Name == label {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.ui.AccessTree())
}

func (s *errScene) drew(text string) bool { return strings.Contains(s.frameText(), text) }

// editor.md, "Images": "a file a `registry.FileViewer` claims opens in that viewer's view instead of a buffer […]. The
// built-in `image` viewer shows an image centred and fit to the tab, with its dimensions, size and format under it,
// zoomable, and reloads it when the file changes".
func TestImageFileTab(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "assets", "hero.png")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, pngBytes(t, photo(640, 400)), 0o644)
	s := newScene(t, "", dir, render.Point{X: 1200, Y: 800})
	s.now = at(9, 0, 0)
	s.run("editor.open", registry.Args{"path": "assets/hero.png", "line": 3})
	s.frames(3)
	if !s.showing(ImageView) {
		t.Fatalf("hero.png didn't open in the image view:\n%s", s.frameText())
	}
	for _, want := range []string{"hero.png", "640 × 400 · ", "· PNG"} {
		if !s.drew(want) {
			t.Errorf("the tab lacks %q:\n%s", want, s.frameText())
		}
	}
	if slices.ContainsFunc(s.editor().e.WindowIDs(), func(id vim.WindowID) bool { p, _ := s.editor().e.Pane(id); return p.Name() == "assets/hero.png" }) {
		t.Error("hero.png was read into a buffer too")
	}
	img := s.accessRole(t, "image", "hero.png")
	s.click(centre(img.Bounds))
	s.frames(2)
	if !s.drew("640×400 · 100%") {
		t.Errorf("a click on the image doesn't show it in the lightbox:\n%s", s.frameText())
	}
	if !s.Key("Esc") {
		t.Fatal("the lightbox doesn't take Escape")
	}
	s.frames(2)
	if s.drew("640×400 · 100%") {
		t.Error("Escape leaves the lightbox up")
	}

	// Opened again, it's the same tab; changed on disk, it's read again.
	s.run("editor.open", registry.Args{"path": p})
	os.WriteFile(p, pngBytes(t, photo(320, 200)), 0o644)
	s.now = s.now.Add(2 * time.Second)
	s.frames(2)
	if !s.drew("320 × 200 · ") {
		t.Errorf("the tab didn't follow the file:\n%s", s.frameText())
	}
	if n := strings.Count(s.frameText(), "hero.png"); n > 2 {
		t.Errorf("hero.png is open %d times", n)
	}
}

// editor.md, "Images": "A video, audio, a PDF or another binary file is a card with its name, type and size and
// \"Open in default app\" (`open` on macOS, `xdg-open` on Linux)."
func TestMediaFileCard(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "demo.mp4"), make([]byte, 3<<20), 0o644)
	os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("ab\x00cd"), 0o644)
	s := newScene(t, "", dir, render.Point{X: 1200, Y: 800})
	s.run("editor.open", registry.Args{"path": "demo.mp4"})
	s.frames(3)
	for _, want := range []string{"demo.mp4", "MP4 video · 3.0 MB", "Oriel doesn't play video.", "Open in default app"} {
		if !s.drew(want) {
			t.Errorf("the card lacks %q:\n%s", want, s.frameText())
		}
	}
	s.click(s.textPoint(t, "Open in default app"))
	s.frames(1)
	if want := filepath.Join(dir, "demo.mp4"); !slices.Contains(s.clip.opened, want) {
		t.Errorf("opened %q, want %s", s.clip.opened, want)
	}
	s.run("editor.open", registry.Args{"path": "blob.bin"})
	s.frames(2)
	if !s.drew("binary file · 5 B") {
		t.Errorf("a binary file's card:\n%s", s.frameText())
	}
	// Text still opens as text.
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello\n"), 0o644)
	s.run("editor.open", registry.Args{"path": "notes.txt"})
	s.frames(2)
	if !s.showing(EditorView) || !s.drew("hello") {
		t.Errorf("a text file didn't open in the editor:\n%s", s.frameText())
	}
}

// editor.md, "Images": "an SVG is drawn the same way, as vectors"; one that can't be read is a card saying why.
func TestSVGFileTab(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "logo.svg"), []byte(testBadge), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.svg"), []byte("<svg width="), 0o644)
	s := newScene(t, "", dir, render.Point{X: 1200, Y: 800})
	s.run("editor.open", registry.Args{"path": "logo.svg"})
	s.frames(3)
	if !s.showing(ImageView) {
		t.Fatalf("logo.svg didn't open in the image view:\n%s", s.frameText())
	}
	for _, want := range []string{"90 × 20 · ", "· SVG"} {
		if !s.drew(want) {
			t.Errorf("the tab lacks %q:\n%s", want, s.frameText())
		}
	}
	img := s.accessRole(t, "image", "logo.svg")
	if w := img.Bounds.Dx(); w != 90 {
		t.Errorf("the SVG is %v wide, want its own 90", w)
	}
	s.click(centre(img.Bounds))
	s.frames(2)
	if !s.drew("90×20") {
		t.Errorf("a click on the SVG doesn't show it in the lightbox:\n%s", s.frameText())
	}
	s.Key("Esc")
	s.frames(2)
	s.run("editor.open", registry.Args{"path": "broken.svg"})
	s.frames(2)
	if !s.drew("SVG image") || !s.drew("It can't be read as an image") {
		t.Errorf("a broken SVG's card:\n%s", s.frameText())
	}
}
