package semver

import "testing"

// ref:   semver/semver.md#Semantic Versioning Specification (SemVer)
// quote: "Major, minor, and patch versions are always compared numerically."
func TestCompare(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1}, {"2.1.0", "2.0.9", 1}, {"0.10.0", "0.9.3", 1}, {"v1.2.3", "1.2.3", 0},
		{"1.0.0-alpha", "1.0.0", -1}, {"1.0.0-beta.2", "1.0.0-beta.10", -1}, {"1.0.0-alpha.1", "1.0.0-alpha.beta", -1},
	} {
		a, err := Parse(tc.a)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Parse(tc.b)
		if err != nil {
			t.Fatal(err)
		}
		if got := a.Compare(b); got != tc.want {
			t.Errorf("%s vs %s = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParse(t *testing.T) {
	for _, bad := range []string{"", "1.2", "01.2.3", "1.2.3-", "a.b.c", "1.2.3.4"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) took it", bad)
		}
	}
	if v, err := Parse("v1.2.3-rc.1+build.5"); err != nil || v != (Version{1, 2, 3, "rc.1"}) {
		t.Errorf("Parse = %+v, %v", v, err)
	}
}

func TestBump(t *testing.T) {
	v := Version{0, 4, 2, "rc.1"}
	for part, want := range map[string]string{"patch": "0.4.3", "minor": "0.5.0", "major": "1.0.0"} {
		if got := v.Bump(part).String(); got != want {
			t.Errorf("bump %s = %s, want %s", part, got, want)
		}
	}
}

// ref:   semver/semver.md#Semantic Versioning Specification (SemVer)
// quote: "Build metadata MAY be denoted by appending a plus sign and a series of dot"
func TestParseBuildMetadataWithHyphen(t *testing.T) {
	for in, want := range map[string]Version{
		"1.0.0+build-1":                  {1, 0, 0, ""},
		"1.0.0+21AF26D3----117B344092BD": {1, 0, 0, ""},
		"1.0.0-alpha+exp-sha":            {1, 0, 0, "alpha"},
		"1.0.0-x-y-z+b-1":                {1, 0, 0, "x-y-z"},
	} {
		if v, err := Parse(in); err != nil || v != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, v, err, want)
		}
	}
}
