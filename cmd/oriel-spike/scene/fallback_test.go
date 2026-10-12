package scene

import (
	"testing"

	"gioui.org/font"
)

func TestFallbackLoadsOnFirstNeed(t *testing.T) {
	f, err := NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	// Only JetBrains Mono, with Inter standing in for a system font.
	var mono []font.FontFace
	for _, c := range f.coll {
		if c.Font.Typeface == Mono {
			mono = append(mono, c)
		}
	}
	f.coll = mono
	var asked []rune
	old := fallbackCandidates
	fallbackCandidates = func(r rune) []string {
		asked = append(asked, r)
		return []string{"fonts/inter/Inter-Regular.ttf"}
	}
	defer func() { fallbackCandidates = old }()

	var r rune
	for _, c := range []rune("ẞŊŦǄ₿ꞋḀ") {
		if !f.covers(mono, c) && coversAny(mustParse(t, "fonts/inter/Inter-Regular.ttf"), c) {
			r = c
			break
		}
	}
	if r == 0 {
		t.Skip("no rune in Inter but not JetBrains Mono among the candidates")
	}
	f.Need("plain ascii")
	if len(asked) != 0 {
		t.Fatalf("ASCII asked for fallbacks %q", asked)
	}
	f.Need(string(r))
	if files, _ := f.Fallbacks(); len(files) != 1 || files[0] != "Inter-Regular.ttf" {
		t.Fatalf("fallbacks = %q, want Inter-Regular.ttf", files)
	}
	f.Need(string(r) + string(r))
	if len(asked) != 1 {
		t.Errorf("asked %d times for %q, want once", len(asked), r)
	}
}

func mustParse(t *testing.T, file string) []font.FontFace {
	faces, err := parseFontFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return faces
}
