package theme

import "testing"

// Pick-to-override writes the smallest diff: a value changes in place, a new key goes after its table's last key,
// and only a table that doesn't exist yet is added (theming.md, "Edits are overrides").
func TestOverrideWritesTheSmallestDiff(t *testing.T) {
	const base = `name = "mine"
extends = "oriel"

[dark.semantic]
accent = "ember" # warmer
"bg.base" = "mix(ink, ember, 3%)"

[palette]
ember = "#ff9b45"
`
	for _, tt := range []struct {
		name       string
		v          Variant
		token, val string
		want       string
	}{
		{"in place, comment kept", Dark, "accent", "brand", `name = "mine"
extends = "oriel"

[dark.semantic]
accent = "brand" # warmer
"bg.base" = "mix(ink, ember, 3%)"

[palette]
ember = "#ff9b45"
`},
		{"after the table's last key", Dark, "bg.raised", "lighten(bg.base, 6%)", `name = "mine"
extends = "oriel"

[dark.semantic]
accent = "ember" # warmer
"bg.base" = "mix(ink, ember, 3%)"
"bg.raised" = "lighten(bg.base, 6%)"

[palette]
ember = "#ff9b45"
`},
		{"a palette colour set in the shared table", Dark, "palette:ember", "#ff8000", `name = "mine"
extends = "oriel"

[dark.semantic]
accent = "ember" # warmer
"bg.base" = "mix(ink, ember, 3%)"

[palette]
ember = "#ff8000"
`},
		{"a component token in a new table", Dark, "tab.active.edge", "accent", base + `
[dark.component]
"tab.active.edge" = "accent"
`},
		{"the other variant gets its own table", Light, "accent", "ember", base + `
[light.semantic]
accent = "ember"
`},
		{"a single-variant theme writes shared tables", "", "@comment", "fg.faint", base + `
[syntax]
"@comment" = "fg.faint"
`},
	} {
		got, err := Override([]byte(base), tt.v, tt.token, tt.val)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if string(got) != tt.want {
			t.Errorf("%s:\n%s\nwant:\n%s", tt.name, got, tt.want)
		}
		if _, err := ParseSpec(got); err != nil {
			t.Errorf("%s: wrote a theme that doesn't parse: %v", tt.name, err)
		}
	}
}

func TestOverrideFindsDottedKeysAndStyleTables(t *testing.T) {
	src := "[semantic]\nbg.base = \"ink\"\n\n[syntax]\n\"@comment\" = { fg = \"fg.faint\", italic = true }\n"
	got, err := Override([]byte(src), Dark, "bg.base", "paper")
	if err != nil {
		t.Fatal(err)
	}
	got, err = Override(got, Dark, "@comment", "fg.muted")
	if err != nil {
		t.Fatal(err)
	}
	want := "[semantic]\nbg.base = \"paper\"\n\n[syntax]\n\"@comment\" = { fg = \"fg.muted\", italic = true }\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Reset to inherited removes the override, and the table with it once it's empty.
func TestResetRemovesTheOverride(t *testing.T) {
	src := "extends = \"oriel\"\n\n[dark.semantic]\naccent = \"ember\"\n\n[dark.palette]\nember = \"#ff9b45\"\nink = \"#000\"\n"
	got, err := Reset([]byte(src), Dark, "accent")
	if err != nil {
		t.Fatal(err)
	}
	got, err = Reset(got, Dark, "palette:ink")
	if err != nil {
		t.Fatal(err)
	}
	want := "extends = \"oriel\"\n\n[dark.palette]\nember = \"#ff9b45\"\n"
	if string(got) != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
	if again, err := Reset(got, Dark, "accent"); err != nil || string(again) != want {
		t.Errorf("resetting what isn't overridden changed the file: %q, %v", again, err)
	}
}

// What an override does to the resolved theme: the token's chain now starts in the user's file.
func TestOverrideResolves(t *testing.T) {
	src, err := Override([]byte("name = \"mine\"\nextends = \"oriel\"\n"), Dark, "bg.raised", "lighten(bg.base, 6%)")
	if err != nil {
		t.Fatal(err)
	}
	r := resolve(t, Dark, oriel(t), spec(t, string(src)))
	chain := r.Chain("bg.raised")
	if len(chain) < 2 || chain[0].From != "mine" || chain[0].Expr != "lighten(bg.base, 6%)" || chain[1].Name != "bg.base" {
		t.Errorf("chain %+v", chain)
	}
	if light := resolve(t, Light, oriel(t), spec(t, string(src))); light.Chain("bg.raised")[0].From == "mine" {
		t.Error("a dark override reached the light variant")
	}
}

func TestOverrideNames(t *testing.T) {
	for _, tt := range []struct {
		name string
		want []string
	}{
		{"palette:ember", []string{"palette", "ember"}},
		{"accent", []string{"semantic", "accent"}},
		{"status.working", []string{"semantic", "status.working"}},
		{"thread.card.bg", []string{"component", "thread.card.bg"}},
		{"@keyword.return", []string{"syntax", "@keyword.return"}},
	} {
		Declare("thread.card.bg", "bg.raised")
		if got := Section(tt.name); len(got) != 2 || got[0] != tt.want[0] || got[1] != tt.want[1] {
			t.Errorf("Section(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
