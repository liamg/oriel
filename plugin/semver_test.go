package plugin

import "testing"

// ref:   semver/semver.md#Semantic Versioning Specification (SemVer)
// quote: "Example: 1.0.0-alpha < 1.0.0-alpha.1 < 1.0.0-alpha.beta < 1.0.0-beta < 1.0.0-beta.2 < 1.0.0-beta.11 < 1.0.0-rc.1 < 1.0.0."
func TestVersionPrecedence(t *testing.T) {
	order := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0"}
	for i := range order {
		for j := range order {
			a, err := ParseVersion(order[i])
			if err != nil {
				t.Fatal(err)
			}
			b, _ := ParseVersion(order[j])
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("%s vs %s: %d, want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"1.0", "01.0.0", "1.0.x", ""} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q parsed as a version", bad)
		}
	}
}

func rangeCases(t *testing.T, cases map[string]map[string]bool) {
	t.Helper()
	for r, vs := range cases {
		rg, err := ParseRange(r)
		if err != nil {
			t.Fatalf("%s: %v", r, err)
		}
		for v, want := range vs {
			pv, err := ParseVersion(v)
			if err != nil {
				t.Fatal(err)
			}
			if got := rg.Match(pv); got != want {
				t.Errorf("%s matches %s: %v, want %v", r, v, got, want)
			}
		}
	}
}

// ref:   node-semver/README.md#Caret Ranges `^1.2.3` `^0.2.5` `^0.0.4`
// quote: "* `^0.2.3` := `>=0.2.3 <0.3.0-0`"
func TestCaretRanges(t *testing.T) {
	rangeCases(t, map[string]map[string]bool{
		"^1.2.3": {"1.2.3": true, "1.9.0": true, "2.0.0": false, "1.2.2": false},
		"^0.2.3": {"0.2.3": true, "0.2.9": true, "0.3.0": false},
		"^0.0.3": {"0.0.3": true, "0.0.4": false},
		"^0.4":   {"0.4.0": true, "0.4.7": true, "0.5.0": false},
		"^0.0":   {"0.0.9": true, "0.1.0": false},
		"^1":     {"1.0.0": true, "1.9.9": true, "2.0.0": false},
	})
}

// ref:   node-semver/README.md#Tilde Ranges `~1.2.3` `~1.2` `~1`
// quote: "* `~1.2.3` := `>=1.2.3 <1.(2+1).0` := `>=1.2.3 <1.3.0-0`"
func TestTildeRanges(t *testing.T) {
	rangeCases(t, map[string]map[string]bool{
		"~1.2.3": {"1.2.3": true, "1.2.9": true, "1.3.0": false},
		"~1.2":   {"1.2.0": true, "1.3.0": false},
		"~1":     {"1.5.0": true, "2.0.0": false},
	})
}

// ref:   node-semver/README.md#X-Ranges `1.2.x` `1.X` `1.2.*` `*`
// quote: "* `*` := `>=0.0.0` (Any non-prerelease version satisfies"
func TestXRanges(t *testing.T) {
	rangeCases(t, map[string]map[string]bool{
		"*":     {"0.0.1": true, "9.9.9": true, "1.0.0-rc.1": false},
		"1.x":   {"1.0.0": true, "1.9.0": true, "2.0.0": false},
		"1.2.*": {"1.2.5": true, "1.3.0": false},
		"1.2":   {"1.2.5": true, "1.3.0": false},
		"":      {"3.1.4": true},
	})
}

// ref:   node-semver/README.md#Ranges
// quote: "The range `1.2.7 || >=1.2.9 <2.0.0` would match the versions `1.2.7`, `1.2.9`, and `1.4.6`, but not the versions `1.2.8` or `2.0.0`."
func TestComparatorSets(t *testing.T) {
	rangeCases(t, map[string]map[string]bool{
		"1.2.7 || >=1.2.9 <2.0.0": {"1.2.7": true, "1.2.9": true, "1.4.6": true, "1.2.8": false, "2.0.0": false},
		">1":                      {"2.0.0": true, "3.1.0": true, "1.0.1": false, "1.1.0": false},
	})
}

// ref:   node-semver/README.md#Prerelease Tags
// quote: "For example, the range `>1.2.3-alpha.3` would be allowed to match the version `1.2.3-alpha.7`, but it would *not* be satisfied by `3.4.5-alpha.9`"
func TestPrereleaseInRanges(t *testing.T) {
	rangeCases(t, map[string]map[string]bool{
		">1.2.3-alpha.3": {"1.2.3-alpha.7": true, "3.4.5-alpha.9": false, "3.4.5": true},
	})
}
