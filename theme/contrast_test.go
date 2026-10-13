package theme

import (
	"math/rand"
	"strings"
	"testing"
)

func TestContrastRatio(t *testing.T) {
	white, black := rgba(255, 255, 255, 255), rgba(0, 0, 0, 255)
	// ref:   wcag/contrast-ratio.html#dfn-contrast-ratio
	// quote: "Contrast ratios can range from 1 to 21 (commonly written 1:1 to 21:1)."
	if got := ContrastRatio(white, black); !near(got, 21, 1e-9) {
		t.Errorf("white on black %v", got)
	}
	if got := ContrastRatio(black, white); !near(got, 21, 1e-9) {
		t.Errorf("order matters: %v", got)
	}
	if got := ContrastRatio(white, white); !near(got, 1, 1e-9) {
		t.Errorf("white on white %v", got)
	}
	// ref:   wcag/relative-luminance.html#dfn-relative-luminance
	// quote: "if RsRGB <= 0.04045 then R = RsRGB/12.92 else R = ((RsRGB+0.055)/1.055) ^ 2.4"
	if got := Luminance(rgba(10, 0, 0, 255)); !near(got, 0.2126*(10.0/255)/12.92, 1e-12) {
		t.Errorf("dark red's luminance %v is off the linear segment", got)
	}
	// ref:   wcag/relative-luminance.html#dfn-relative-luminance
	// quote: "L = 0.2126 * R + 0.7152 * G + 0.0722 * B"
	if got := Luminance(rgba(0, 255, 0, 255)); !near(got, 0.7152, 1e-12) {
		t.Errorf("green %v", got)
	}
}

// Lint finds declared pairs that fall under their threshold, and status colours too close to tell apart.
func TestLint(t *testing.T) {
	r := resolve(t, Dark, oriel(t), spec(t, `name = "murky"
[semantic]
"fg.muted" = "#202020"
"status.ci" = "#4ccaf0"
`))
	var got []string
	for _, f := range Lint(r) {
		got = append(got, f.Message)
	}
	all := strings.Join(got, "\n")
	// ref:   wcag/contrast-minimum.html#contrast-minimum
	// quote: "has a contrast ratio of at least 4.5:1"
	if !strings.Contains(all, "fg.muted on bg.surface is 1.") || !strings.Contains(all, "under 4.5:1") {
		t.Errorf("no finding for fg.muted:\n%s", all)
	}
	if !strings.Contains(all, "status.ci and status.working") {
		t.Errorf("no finding for two statuses a hair apart:\n%s", all)
	}
	for _, f := range Lint(r) {
		if f.Token == "" || f.Fix == "" && f.Kind == "contrast" {
			t.Errorf("finding without a token or fix: %+v", f)
		}
	}
}

// A fix the lint offers brings the pair over its threshold.
func TestLintFix(t *testing.T) {
	r := resolve(t, Dark, oriel(t), spec(t, "name = \"murky\"\n[semantic]\n\"fg.muted\" = \"#303030\"\n"))
	for _, f := range Lint(r) {
		if f.Token != "fg.muted" {
			continue
		}
		fixed := resolve(t, Dark, oriel(t), spec(t, "name = \"fixed\"\n[semantic]\n\"fg.muted\" = \""+f.Fix+"\"\n"))
		fg, _ := fixed.Color("fg.muted")
		bg, _ := fixed.Color("bg.surface")
		if c := ContrastRatio(fg, bg); c < 4.5 {
			t.Errorf("fix %q gives %.2f", f.Fix, c)
		}
		return
	}
	t.Fatal("no finding for fg.muted")
}

// Pick mode paints every token in a colour of its own and reads back what's under the pointer.
func TestProbe(t *testing.T) {
	r := resolve(t, Dark, oriel(t))
	p := Probe(r)
	seen := map[Color]string{}
	for _, tok := range Tokens() {
		c := p.C(tok.ID)
		if c.A != 255 {
			t.Fatalf("%s probes translucent", tok.Name)
		}
		if other, ok := seen[c]; ok {
			t.Fatalf("%s and %s probe the same", tok.Name, other)
		}
		seen[c] = tok.Name
		if got, ok := p.Probed(c); !ok || got != tok.Name {
			t.Errorf("%s probed back as %q", tok.Name, got)
		}
	}
	if got, ok := p.Probed(p.Syntax("@keyword.return").FG); !ok || got != "@keyword" {
		t.Errorf("@keyword.return probed back as %q", got)
	}
	if p.Effect("glow") != 0 {
		t.Error("probe left the glow on")
	}
	// Antialiased edges blend two probes; a blend shouldn't read as a third token.
	rng := rand.New(rand.NewSource(1))
	ts, wrong := Tokens(), 0
	for range 10000 {
		a, b := p.C(ts[rng.Intn(len(ts))].ID), p.C(ts[rng.Intn(len(ts))].ID)
		mix := rgba(uint8((int(a.R)+int(b.R))/2), uint8((int(a.G)+int(b.G))/2), uint8((int(a.B)+int(b.B))/2), 255)
		if mix != a && mix != b {
			if _, ok := p.Probed(mix); ok {
				wrong++
			}
		}
	}
	if wrong > 10 {
		t.Errorf("%d of 10000 blends read as a token", wrong)
	}
}

// Every built-in theme resolves in both variants with every declared token set.
func TestBuiltinsResolve(t *testing.T) {
	for _, src := range Builtins() {
		st, err := Stack(Builtins(), src.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []Variant{Dark, Light} {
			r := resolve(t, v, st...)
			for _, tok := range Tokens() {
				if r.C(tok.ID).A == 0 && !strings.HasPrefix(tok.Default, "alpha(") {
					t.Errorf("%s %s: %s is transparent", src.Name(), v, tok.Name)
				}
			}
		}
	}
}
