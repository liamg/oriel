package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// imageRepo is a branch off main that redraws logo.png, adds new.png and deletes old.png.
func imageRepo(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{"logo.png": string(pngBytes(t, photo(320, 200))), "old.png": string(pngBytes(t, photo(64, 64))), "README.md": "hi\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "art")
	r.Write(map[string]string{"logo.png": string(pngBytes(t, photo(400, 200))), "new.png": string(pngBytes(t, photo(48, 32)))})
	r.Remove("old.png")
	r.Commit("Claude", "new logo")
	return r
}

// imageDiffScene is the branch diff of imageRepo's file open, its images drawn.
func imageDiffScene(t *testing.T, file string) (*errScene, *gittest.Repo) {
	r := imageRepo(t)
	s := newScene(t, "", r.Dir, render.Point{X: 1400, Y: 900})
	s.sh.git.wait()
	s.run("diff.open", registry.Args{"path": filepath.Join(r.Dir, file)})
	if !s.Until(5*time.Second, func() bool {
		s.diff().wait()
		return s.drew("· ") && (s.drew("OLD") || s.drew("ADDED") || s.drew("DELETED"))
	}) {
		t.Fatalf("%s's images never showed:\n%s", file, s.frameText())
	}
	s.frames(2)
	return s, r
}

// editor.md, "Images": "a changed image is drawn old and new, Side by side (each labelled with its dimensions and
// size) or as a Slider (the two overlaid, a draggable divider with the old left of it and the new right); an added or
// deleted image shows its one side. Both are one lightbox group."
func TestImageDiff(t *testing.T) {
	s, _ := imageDiffScene(t, "logo.png")
	for _, want := range []string{"Side by side", "Slider", "OLD", "NEW", "320×200 · ", "400×200 · "} {
		if !s.drew(want) {
			t.Errorf("the image diff lacks %q:\n%s", want, s.frameText())
		}
	}
	if s.drew("A binary file") {
		t.Error("the image diff still says it's binary")
	}
	s.click(centre(s.accessRole(t, "image", "NEW logo.png").Bounds))
	s.frames(2)
	if !s.drew("2 / 2") || !s.drew("NEW logo.png") {
		t.Errorf("a click on the new version doesn't show the two in the lightbox:\n%s", s.frameText())
	}
	s.Key("Esc")
	s.frames(2)

	s.click(s.textPoint(t, "Slider"))
	s.frames(2)
	slider := s.accessRole(t, "slider", "logo.png")
	if slider.Value != "50%" {
		t.Errorf("the slider starts at %q", slider.Value)
	}
	r := slider.Bounds
	from := render.Point{X: (r.Min.X + r.Max.X) / 2, Y: r.Min.Y + 20}
	to := render.Point{X: r.Min.X + r.Dx()*.25, Y: from.Y}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: to, Buttons: render.ButtonPrimary})
	s.frame()
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: to, Buttons: render.ButtonPrimary})
	s.frames(2)
	if v := s.accessRole(t, "slider", "logo.png").Value; v != "25%" {
		t.Errorf("dragged to a quarter, the slider is at %q", v)
	}
}

func TestImageDiffAddedAndDeleted(t *testing.T) {
	s, _ := imageDiffScene(t, "new.png")
	if !s.drew("ADDED") || !s.drew("48×32 · ") || s.drew("Slider") {
		t.Errorf("an added image:\n%s", s.frameText())
	}
	s, _ = imageDiffScene(t, "old.png")
	if !s.drew("DELETED") || !s.drew("64×64 · ") || s.drew("Slider") {
		t.Errorf("a deleted image:\n%s", s.frameText())
	}
}
