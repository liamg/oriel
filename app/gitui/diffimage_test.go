package gitui_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// imageRepo is a branch off main that redraws logo.png, adds new.png and deletes old.png.
func imageRepo(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{"logo.png": string(rig.PNG(t, rig.Photo(320, 200))), "old.png": string(rig.PNG(t, rig.Photo(64, 64))), "README.md": "hi\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "art")
	r.Write(map[string]string{"logo.png": string(rig.PNG(t, rig.Photo(400, 200))), "new.png": string(rig.PNG(t, rig.Photo(48, 32)))})
	r.Remove("old.png")
	r.Commit("Claude", "new logo")
	return r
}

// imageDiffScene is the branch diff of imageRepo's file open, its images drawn.
func imageDiffScene(t *testing.T, file string) (*apptest.Scene, *gittest.Repo) {
	r := imageRepo(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1400, Y: 900}})
	gitOf(s).Wait()
	s.Run("diff.open", registry.Args{"path": filepath.Join(r.Dir, file)})
	if !s.Headless.Until(5*time.Second, func() bool {
		diffOf(s).Wait()
		return s.Has("· ") && (s.Has("OLD") || s.Has("ADDED") || s.Has("DELETED"))
	}) {
		t.Fatalf("%s's images never showed:\n%s", file, s.Text())
	}
	s.Frames(2)
	return s, r
}

// A changed image is drawn old and new, Side by side (each labelled with its dimensions and size) or as a Slider
// (the two overlaid, a draggable divider with the old left of it and the new right); an added or deleted image shows
// its one side. Both are one lightbox group.
func TestImageDiff(t *testing.T) {
	s, _ := imageDiffScene(t, "logo.png")
	for _, want := range []string{"Side by side", "Slider", "OLD", "NEW", "320×200 · ", "400×200 · "} {
		if !s.Has(want) {
			t.Errorf("the image diff lacks %q:\n%s", want, s.Text())
		}
	}
	if s.Has("A binary file") {
		t.Error("the image diff still says it's binary")
	}
	s.Click(apptest.Centre(s.NodeRole("image", "NEW logo.png").Bounds), 0)
	s.Frames(2)
	if !s.Has("2 / 2") || !s.Has("NEW logo.png") {
		t.Errorf("a click on the new version doesn't show the two in the lightbox:\n%s", s.Text())
	}
	s.Key("Esc")
	s.Frames(2)

	s.Click(s.TextPoint("Slider"), 0)
	s.Frames(2)
	slider := s.NodeRole("slider", "logo.png")
	if slider.Value != "50%" {
		t.Errorf("the slider starts at %q", slider.Value)
	}
	r := slider.Bounds
	from := render.Point{X: (r.Min.X + r.Max.X) / 2, Y: r.Min.Y + 20}
	to := render.Point{X: r.Min.X + r.Dx()*.25, Y: from.Y}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: to, Buttons: render.ButtonPrimary})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: to, Buttons: render.ButtonPrimary})
	s.Frames(2)
	if v := s.NodeRole("slider", "logo.png").Value; v != "25%" {
		t.Errorf("dragged to a quarter, the slider is at %q", v)
	}
}

func TestImageDiffAddedAndDeleted(t *testing.T) {
	s, _ := imageDiffScene(t, "new.png")
	if !s.Has("ADDED") || !s.Has("48×32 · ") || s.Has("Slider") {
		t.Errorf("an added image:\n%s", s.Text())
	}
	s, _ = imageDiffScene(t, "old.png")
	if !s.Has("DELETED") || !s.Has("64×64 · ") || s.Has("Slider") {
		t.Errorf("a deleted image:\n%s", s.Text())
	}
}
