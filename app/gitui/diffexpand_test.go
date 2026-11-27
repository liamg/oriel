package gitui_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// twoFileChange is a repo whose change touches two files, so only the first opens by default.
func twoFileChange(t *testing.T) *gittest.Repo {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "first\n", "b.txt": "first\n"})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"a.txt": "first\nalpha\n", "b.txt": "first\nbravo\n"})
	return r
}

// docs/configuration.md: "diff.expanded: false: the review diff opens with only the first file expanded; true, every
// file". Set in settings.toml, every file shows its lines.
func TestTheDiffExpandedSettingOpensEveryFile(t *testing.T) {
	r := twoFileChange(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.WriteConfig(map[string]string{"settings.toml": "[diff]\nexpanded = true\n"})
	s.Run("settings.reload", nil)
	s.Quiet()
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("both files drawn", func() bool { return strings.Contains(s.Text(), "alpha") && strings.Contains(s.Text(), "bravo") })
}

// help files/diff#files: "A click on the header shows or folds it": with diff.expanded on, a click folds an open file.
func TestAFileFoldsWithTheDiffExpandedSettingOn(t *testing.T) {
	r := twoFileChange(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.WriteConfig(map[string]string{"settings.toml": "[diff]\nexpanded = true\n"})
	s.Run("settings.reload", nil)
	s.Quiet()
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("b.txt drawn", func() bool { return strings.Contains(s.Text(), "bravo") })
	s.Quiet()
	s.Run("diff.expand", registry.Args{"path": "b.txt"})
	s.Until("b.txt folded", func() bool { return !strings.Contains(s.Text(), "bravo") })
	if !strings.Contains(s.Text(), "alpha") {
		t.Errorf("a.txt folded too:\n%s", s.Text())
	}
}

// help files/diff#files: "Expand all at the top of the diff, or zA (diff.expand with all), shows every file; with
// every one shown, Fold all (or zA again) folds them."
//
// ref: fold.txt *zA*
// quote: "When on a closed fold: open it recursively. When on an open fold: close it recursively"
func TestTheDiffExpandsAndFoldsEveryFile(t *testing.T) {
	r := twoFileChange(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("a.txt drawn", func() bool { return strings.Contains(s.Text(), "alpha") })
	s.Quiet()
	if strings.Contains(s.Text(), "bravo") {
		t.Fatalf("b.txt is open before expanding:\n%s", s.Text())
	}
	s.Press("zA")
	s.Until("both files drawn", func() bool { return strings.Contains(s.Text(), "alpha") && strings.Contains(s.Text(), "bravo") })
	s.Quiet()
	s.Press("zA")
	s.Until("both files folded", func() bool { return !strings.Contains(s.Text(), "alpha") && !strings.Contains(s.Text(), "bravo") })
}

// help files/diff#files: "Expand all at the top of the diff, or zA (diff.expand with all), shows every file; with
// every one shown, Fold all (or zA again) folds them."
func TestTheDiffsHeaderButtonExpandsAndFoldsEveryFile(t *testing.T) {
	r := twoFileChange(t)
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("a.txt drawn", func() bool { return strings.Contains(s.Text(), "alpha") })
	s.Quiet()
	b := s.FindRole(render.RoleButton, "Expand all")
	if b == nil {
		t.Fatalf("no Expand all button:\n%s", s.Text())
	}
	s.Click(apptest.Centre(b.Bounds), 0)
	s.Until("both files drawn", func() bool { return strings.Contains(s.Text(), "alpha") && strings.Contains(s.Text(), "bravo") })
	s.Quiet()
	b = s.FindRole(render.RoleButton, "Fold all")
	if b == nil {
		t.Fatalf("no Fold all button with every file shown:\n%s", s.Text())
	}
	s.Click(apptest.Centre(b.Bounds), 0)
	s.Until("both files folded", func() bool { return !strings.Contains(s.Text(), "alpha") && !strings.Contains(s.Text(), "bravo") })
}
