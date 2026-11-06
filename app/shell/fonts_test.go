package shell_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// facesOf are the families the frame's letters in role are drawn with, each once: a symbol the family lacks comes
// from another face.
func facesOf(s *apptest.Scene, role string) []string {
	var out []string
	for _, op := range s.List().Ops {
		if op.Kind != render.OpGlyphs {
			continue
		}
		if fam := op.Run.Font.Family; fam != role && !(role == typeset.UI && fam == "") {
			continue
		}
		runes := []rune(op.Run.Text)
		for _, g := range op.Run.Glyphs {
			if int(g.Rune) >= len(runes) || !unicode.IsLetter(runes[g.Rune]) || runes[g.Rune] > unicode.MaxLatin1 {
				continue
			}
			if g.Face != nil && !slices.Contains(out, g.Face.Family) {
				out = append(out, g.Face.Family)
			}
		}
	}
	slices.Sort(out)
	return out
}

// ui.font sets the UI's text in a family installed on the machine, which it suggests; set back to empty, Oriel's
// own; and one that isn't installed says so.
func TestUIFontIsAnInstalledFamily(t *testing.T) {
	dir := t.TempDir()
	font, err := os.ReadFile(filepath.Join("..", "..", "render", "typeset", "fonts", "space-grotesk", "SpaceGrotesk-Medium.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SpaceGrotesk-Medium.ttf"), font, 0o644); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), FontDirs: []string{dir}})
	s.Run("settings.open", nil) // the settings page: the UI's text, where the font is chosen
	spec, ok := s.Registry().Options().Spec("ui.font")
	if !ok || spec.Choices == nil {
		t.Fatal("no ui.font with choices")
	}
	spec.Choices() // the families are read off the loop the first time they're asked for
	s.Quiet()
	if got := spec.Choices(); !slices.Equal(got, []string{"Space Grotesk"}) {
		t.Fatalf("ui.font suggests %v, want the installed Space Grotesk", got)
	}
	s.Quiet()
	if got := facesOf(s, typeset.UI); !slices.Equal(got, []string{typeset.UI}) {
		t.Fatalf("before a font is chosen, the UI's text is in %v", got)
	}
	opts := s.Registry().Options()
	if err := opts.Set("ui.font", registry.LayerUser, registry.At{}, "Space Grotesk"); err != nil {
		t.Fatal(err)
	}
	s.Quiet()
	if got := facesOf(s, typeset.UI); !slices.Equal(got, []string{"Space Grotesk"}) {
		t.Errorf("with ui.font Space Grotesk, the UI's text is in %v", got)
	}
	if got := facesOf(s, typeset.Mono); slices.Contains(got, "Space Grotesk") {
		t.Errorf("fixed-width text took ui.font too: %v", got)
	}
	if err := opts.Set("ui.font", registry.LayerUser, registry.At{}, ""); err != nil {
		t.Fatal(err)
	}
	s.Quiet()
	if got := facesOf(s, typeset.UI); !slices.Equal(got, []string{typeset.UI}) {
		t.Errorf("ui.font emptied, the UI's text is in %v", got)
	}
	if err := opts.Set("ui.font_mono", registry.LayerUser, registry.At{}, "Comic Sans"); err != nil {
		t.Fatal(err)
	}
	s.Quiet()
	if !slices.ContainsFunc(s.Store().State().Notifications, func(n core.Notification) bool {
		return n.ID == "ui.font" && strings.Contains(n.Text, `"Comic Sans"`)
	}) {
		t.Errorf("a family that isn't installed said nothing: %+v", s.Store().State().Notifications)
	}
}
