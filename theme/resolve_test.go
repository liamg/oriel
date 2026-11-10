package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

func spec(t *testing.T, src string) *Spec {
	t.Helper()
	s, err := ParseSpec([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func oriel(t *testing.T) *Spec {
	t.Helper()
	st, err := Stack(Builtins(), "oriel")
	if err != nil {
		t.Fatal(err)
	}
	return st[0]
}

func resolve(t *testing.T, v Variant, stack ...*Spec) *Resolved {
	t.Helper()
	r, err := Resolve(stack, v)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func hexOf(t *testing.T, r *Resolved, name string) string {
	t.Helper()
	c, ok := r.Color(name)
	if !ok {
		t.Fatalf("no %s", name)
	}
	return Hex(c)
}

// The built-in theme is the mockups' tokens: base.css for dark, 17-light.html for light.
func TestBuiltinTokens(t *testing.T) {
	for _, tt := range []struct {
		v    Variant
		want map[string]string
	}{
		{Dark, map[string]string{
			"bg.base": "#060606", "bg.surface": "#0c0c0b", "bg.raised": "#121210", "line": "#24221d", "line.strong": "#332f27",
			"fg": "#ece6d8", "fg.muted": "#b3ab99", "fg.faint": "#7c7566", "fg.ghost": "#4f4a40",
			"accent": "#ffd23f", "brand": "#ff8a1f", "status.working": "#4cc9f0", "status.needs_you": "#ff5f6d",
			"status.quiet": "#4fe0a0", "status.reviewer": "#ff7ad6", "status.permission": "#b98aff", "status.ci": "#5c8dff",
			"accent.bg": "#1f1c0f", // 8% accent over bg.surface, opaque
		}},
		{Light, map[string]string{
			"bg.base": "#fbfaf7", "fg": "#1b2129", "accent": "#0a78a8", "status.working": "#d9661a", "line": "#dcd7cc",
		}},
	} {
		r := resolve(t, tt.v, oriel(t))
		for name, want := range tt.want {
			if got := hexOf(t, r, name); got != want {
				t.Errorf("%s %s = %s, want %s", tt.v, name, got, want)
			}
		}
	}
	if r := resolve(t, Dark, oriel(t)); r.Effect("glow") != 1 || r.Effect("grid") != 0 {
		t.Errorf("dark effects: glow %v grid %v", r.Effect("glow"), r.Effect("grid"))
	}
	if r := resolve(t, Light, oriel(t)); r.Effect("glow") != 0 || r.Effect("grid") != 1 {
		t.Errorf("light effects: glow %v grid %v", r.Effect("glow"), r.Effect("grid"))
	}
}

// Token resolution: each row layers a theme over the built-in one and reads a token.
func TestResolutionTable(t *testing.T) {
	Declare("tab.active.edge", "accent")
	Declare("thread.card.bg", "bg.raised")
	for _, tt := range []struct {
		name, theme, token, want string
	}{
		{"a palette change reaches every token built on it", "[palette]\naccent = \"#ff9b45\"", "tab.active.edge", "#ff9b45"},
		{"a semantic override names a palette colour", "[palette]\nember = \"#ff9b45\"\n[semantic]\naccent = \"ember\"", "tab.active.edge", "#ff9b45"},
		{"a token's own name is the palette colour", "[palette]\naccent = \"#00ff00\"\n[semantic]\naccent = \"accent\"", "accent", "#00ff00"},
		{"a component override wins over its default", "[component]\n\"tab.active.edge\" = \"status.working\"", "tab.active.edge", "#4cc9f0"},
		{"dotted keys read as token names", "[semantic]\nbg.raised = \"#222222\"", "thread.card.bg", "#222222"},
		{"0% mix is the first colour", "[semantic]\n\"bg.base\" = \"mix(bg0, accent, 0%)\"", "bg.base", "#060606"},
		{"100% mix is the second", "[semantic]\n\"bg.base\" = \"mix(bg0, accent, 100%)\"", "bg.base", "#ffd23f"},
		{"alpha on a surface is opaque", "[component]\n\"thread.card.bg\" = { color = \"alpha(accent, 50%)\", on = \"bg.base\" }", "thread.card.bg", "#836c23"}, // (255+6)/2, (210+6)/2, (63+6)/2
		{"over composites in sRGB", "[component]\n\"thread.card.bg\" = \"over(alpha(#ffffff, 50%), #000000)\"", "thread.card.bg", "#808080"},
		{"[dark] applies to dark", "[dark.semantic]\naccent = \"#123456\"", "accent", "#123456"},
		{"[light] doesn't", "[light.semantic]\naccent = \"#123456\"", "accent", "#ffd23f"},
		{"literals: oklch", "[semantic]\naccent = \"oklch(0.452 0.313 264.1)\"", "accent", "#0100ff"},
	} {
		r := resolve(t, Dark, oriel(t), spec(t, "name = \"t\"\n"+tt.theme))
		if got := hexOf(t, r, tt.token); got != tt.want {
			t.Errorf("%s: %s = %s, want %s", tt.name, tt.token, got, tt.want)
		}
	}
}

func TestLighten(t *testing.T) {
	r := resolve(t, Dark, oriel(t), spec(t, "[semantic]\n\"bg.base\" = \"lighten(bg0, 10%)\"\n\"bg.surface\" = \"darken(lighten(bg0, 10%), 10%)\""))
	base, _ := r.Color("bg.base")
	if l := ToOKLCH(base).L - ToOKLCH(rgba(6, 6, 6, 255)).L; !near(l, 0.1, 0.004) {
		t.Errorf("lighten(bg0, 10%%) moved L by %v", l)
	}
	if got := hexOf(t, r, "bg.surface"); got != "#060606" {
		t.Errorf("darken undid lighten to %s", got)
	}
}

func TestResolveErrors(t *testing.T) {
	for _, tt := range []struct{ theme, want string }{
		{"[semantic]\naccent = \"fg.muted\"\n\"fg.muted\" = \"accent.strong\"\n\"accent.strong\" = \"accent\"", "cycle"},
		{"[semantic]\naccent = \"nope\"", `no colour "nope"`},
		{"[palette]\na = \"b\"\nb = \"a\"\n[semantic]\naccent = \"a\"", "cycle"},
		{"[semantic]\naccent = \"mix(fg, bg0)\"", "takes 3 arguments"},
		{"[semantic]\naccent = \"blend(fg, bg0, 1)\"", "unknown function"},
	} {
		_, err := Resolve([]*Spec{oriel(t), spec(t, tt.theme)}, Dark)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%q: err = %v, want %q", tt.theme, err, tt.want)
		}
	}
	for _, bad := range []string{"[colours]\na = \"#fff\"", "variant = \"sepia\"", "[syntax]\nkeyword = \"#fff\"", "[effects]\nglow = \"lots\""} {
		if _, err := ParseSpec([]byte(bad)); err == nil {
			t.Errorf("ParseSpec(%q) succeeded", bad)
		}
	}
}

func TestSyntaxFallsBackAlongItsPath(t *testing.T) {
	r := resolve(t, Dark, oriel(t), spec(t, "[syntax]\n\"@keyword.return\" = { fg = \"red\", bold = true }"))
	if s := r.Syntax("@keyword.return"); Hex(s.FG) != "#ff5f6d" || !s.Bold {
		t.Errorf("@keyword.return = %+v", s)
	}
	if s := r.Syntax("@keyword.return.go"); Hex(s.FG) != "#ff5f6d" {
		t.Errorf("@keyword.return.go doesn't fall back to @keyword.return: %+v", s)
	}
	if s := r.Syntax("@keyword.import"); Hex(s.FG) != "#c792ff" {
		t.Errorf("@keyword.import doesn't fall back to @keyword: %+v", s)
	}
	if s := r.Syntax("@comment.doc"); !s.Italic {
		t.Error("@comment.doc lost @comment's italic")
	}
	if s := r.Syntax("@nothing"); Hex(s.FG) != "#ece6d8" {
		t.Errorf("an unknown group is %s, want fg", Hex(s.FG))
	}
}

func TestChain(t *testing.T) {
	Declare("tab.active.edge", "accent")
	r := resolve(t, Dark, oriel(t), spec(t, "name = \"mine\"\n[palette]\nember = \"#ff9b45\"\n[semantic]\naccent = \"ember\""))
	var got []string
	for _, l := range r.Chain("tab.active.edge") {
		got = append(got, l.Name+" = "+l.Expr+" ("+l.From+")")
	}
	want := "tab.active.edge = accent (default) ← accent = ember (mine) ← palette:ember = #ff9b45 (mine)"
	if strings.Join(got, " ← ") != want {
		t.Errorf("chain:\n%s\nwant:\n%s", strings.Join(got, " ← "), want)
	}
}

func TestExtendsAndStack(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) Source {
		p := filepath.Join(dir, name+".toml")
		os.WriteFile(p, []byte(src), 0o644)
		return File{Path: p}
	}
	sources := append(Builtins(),
		write("warm", "name = \"warm\"\nextends = \"oriel\"\n[palette]\naccent = \"#ff9b45\""),
		write("warmer", "name = \"warmer\"\nextends = \"warm\"\n[semantic]\n\"bg.base\" = \"mix(bg0, accent, 3%)\""),
		write("loop", "name = \"loop\"\nextends = \"loop\""),
	)
	st, err := Stack(sources, "warmer")
	if err != nil {
		t.Fatal(err)
	}
	if len(st) != 3 || st[0].Name != "oriel" || st[2].Name != "warmer" {
		t.Fatalf("stack %v", st)
	}
	r := resolve(t, Dark, st...)
	if got := hexOf(t, r, "accent"); got != "#ff9b45" {
		t.Errorf("accent %s, from warm", got)
	}
	if got := hexOf(t, r, "fg"); got != "#ece6d8" {
		t.Errorf("fg %s, from oriel", got)
	}
	if _, err := Stack(sources, "loop"); err == nil {
		t.Error("a theme extending itself loaded")
	}
	if _, err := Stack(sources, "missing"); err == nil {
		t.Error("a missing theme loaded")
	}
}

func TestFileWatch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "live.toml")
	os.WriteFile(p, []byte("name = \"live\"\n"), 0o644)
	clk := clocktest.New(time.Unix(0, 0))
	changed := 0
	cancel := File{Path: p, PollEvery: time.Second, Clock: clk}.Watch(func() { changed++ })
	defer cancel()
	// Straight away, and the same size: a save right after Watch returns still counts.
	os.WriteFile(p, []byte("name = \"evil\"\n"), 0o644)
	clk.Advance(time.Second)
	if changed != 1 {
		t.Fatalf("%d changes seen after a save and a poll; want 1", changed)
	}
	clk.Advance(time.Second)
	if changed != 1 {
		t.Errorf("%d changes seen after a poll with no save; want 1", changed)
	}
	cancel()
	os.WriteFile(p, []byte("name = \"gone\"\n"), 0o644)
	clk.Advance(time.Second)
	if changed != 1 || clk.Waiting() != 0 {
		t.Errorf("after cancel: %d changes, %d polls waiting", changed, clk.Waiting())
	}
}

func BenchmarkResolve(b *testing.B) {
	st, _ := Stack(Builtins(), "oriel")
	for b.Loop() {
		Resolve(st, Dark)
	}
}

func TestTokenName(t *testing.T) {
	if Accent.Name() != "accent" || TokenID(-1).Name() != "" {
		t.Errorf("%q", Accent.Name())
	}
}

// A theme with no [dark]/[light] tables resolves at its own variant, whatever is asked; one with them, or with no
// variant of its own, follows what's asked.
func TestEffectiveVariant(t *testing.T) {
	dark := spec(t, "extends = \"oriel\"\nvariant = \"dark\"\n[palette]\nbg0 = \"#2e3440\"")
	plain := spec(t, "extends = \"oriel\"\n[semantic]\naccent = \"red\"")
	for _, tt := range []struct {
		name  string
		stack []*Spec
		want  Variant
		got   Variant
	}{
		{"oriel follows light", []*Spec{oriel(t)}, Light, Light},
		{"oriel follows dark", []*Spec{oriel(t)}, Dark, Dark},
		{"a dark-only theme stays dark", []*Spec{oriel(t), dark}, Light, Dark},
		{"a theme with no variant follows what it extends", []*Spec{oriel(t), plain}, Light, Light},
		{"a theme over a dark-only one stays dark", []*Spec{oriel(t), dark, plain}, Light, Dark},
	} {
		if v := Effective(tt.stack, tt.want); v != tt.got {
			t.Errorf("%s: Effective(%s) = %s, want %s", tt.name, tt.want, v, tt.got)
		}
	}
}
