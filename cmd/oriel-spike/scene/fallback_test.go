package scene

import (
	"testing"
	"time"

	"gioui.org/font"
)

const interFile = "fonts/inter/Inter-Regular.ttf"

// interOnly finds a rune Inter has and JetBrains Mono lacks.
func interOnly(t *testing.T, ts *typesetter) rune {
	mono := ts.face(font.Font{Typeface: Mono})
	inter := ts.face(font.Font{Typeface: UI})
	for _, r := range "ẞŊŦǄ₿ꞋḀ" {
		if !ts.supported(mono, r) && ts.supported(inter, r) {
			return r
		}
	}
	t.Skip("no rune in Inter but not JetBrains Mono among the candidates")
	return 0
}

func TestLoadFaceMapsAndPicksTheCoveringFace(t *testing.T) {
	ts, err := newTypesetter(newAtlasOps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := interOnly(t, ts)
	face, i := loadFace(interFile, r, map[string]bool{})
	if face == nil || i != 0 {
		t.Fatalf("loadFace(%q) = %v, %d", r, face, i)
	}
	if _, ok := face.NominalGlyph(r); !ok {
		t.Error("loaded face lacks the rune")
	}
	if face, _ := loadFace(interFile, r, map[string]bool{interFile + "#0": true}); face != nil {
		t.Error("loaded a face already tried")
	}
	if face, _ := loadFace(interFile, 0x10FFFD, map[string]bool{}); face != nil {
		t.Error("loaded a face for a rune it doesn't have")
	}
}

func TestFallbackLandsOffTheFramePath(t *testing.T) {
	f, err := NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	ts, err := newTypesetter(newAtlasOps(), f)
	if err != nil {
		t.Fatal(err)
	}
	r := interOnly(t, ts)
	// Only JetBrains Mono is bundled here, with Inter standing in for a system font.
	mono := ts.face(font.Font{Typeface: Mono})
	ts.faces, ts.fonts, ts.bundled = ts.faces[mono:mono+1], ts.fonts[mono:mono+1], 1

	var asked []rune
	old := fallbackCandidates
	fallbackCandidates = func(r rune) []string { asked = append(asked, r); return []string{interFile} }
	defer func() { fallbackCandidates = old }()
	landed := make(chan struct{}, 1)
	f.OnLoad = func() { landed <- struct{}{} }

	if got := ts.faceFor(0, 'a'); got != 0 {
		t.Fatalf("ASCII went to face %d", got)
	}
	if got := ts.faceFor(0, r); got != -1 {
		t.Fatalf("missing rune went to face %d before any fallback loaded", got)
	}
	ts.faceFor(0, r) // asking again doesn't queue a second load
	select {
	case <-landed:
	case <-time.After(5 * time.Second):
		t.Fatal("fallback never landed")
	}
	if got := ts.faceFor(0, r); got != -1 {
		t.Fatal("a fallback face was used before the next frame took it in")
	}
	ts.beginFrame()
	if got := ts.faceFor(0, r); got != 1 {
		t.Fatalf("after landing, %q went to face %d, want the fallback (1)", r, got)
	}
	if files, _ := f.Fallbacks(); len(files) != 1 || files[0] != "Inter-Regular.ttf" {
		t.Errorf("fallbacks = %q", files)
	}
	if len(asked) != 1 {
		t.Errorf("asked for candidates %d times, want once", len(asked))
	}
}
