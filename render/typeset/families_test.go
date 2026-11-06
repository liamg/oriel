package typeset

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// installed copies a bundled font into a folder of its own, as the machine's installed fonts for t.
func installed(t *testing.T, ts *Typesetter, files ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		b, err := fontFS.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(f)), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ts.SetFontDirs([]string{dir})
}

// A role drawn with an installed family takes its faces, a family that isn't installed is refused, and "" puts
// the bundled face back; each change moves Gen, so what was measured with the old faces is known to be stale.
func TestARoleDrawsWithAnInstalledFamily(t *testing.T) {
	ts, err := NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	files, _ := fs.Glob(fontFS, "fonts/space-grotesk/*.ttf")
	installed(t, ts, files...)
	if fams := ts.Families(); !slices.Equal(fams, []string{"Space Grotesk"}) {
		t.Fatalf("installed families %v", fams)
	}
	ui := FontSpec{Family: UI, Size: 13}
	if f := ts.Shape("Ship it", ui).Glyphs[0].Face.Family; f != UI {
		t.Fatalf("the UI draws with %q before a family is chosen", f)
	}
	gen := ts.Gen()
	if err := ts.Use(UI, "Space Grotesk"); err != nil {
		t.Fatal(err)
	}
	if f := ts.Shape("Ship it", ui).Glyphs[0].Face.Family; f != "Space Grotesk" {
		t.Errorf("the UI draws with %q, want the installed Space Grotesk", f)
	}
	if f := ts.Shape("Ship it", FontSpec{Family: Mono, Size: 13}).Glyphs[0].Face.Family; f != Mono {
		t.Errorf("code draws with %q: only the UI's role changed", f)
	}
	if ts.Gen() == gen {
		t.Error("Gen didn't move with the UI's faces")
	}
	if err := ts.Use(Mono, "Comic Sans"); err == nil {
		t.Error("a family that isn't installed was taken")
	}
	if err := ts.Use(UI, ""); err != nil {
		t.Fatal(err)
	}
	if f := ts.Shape("Ship it", ui).Glyphs[0].Face.Family; f != UI {
		t.Errorf("back to the bundled face, the UI draws with %q", f)
	}
}
