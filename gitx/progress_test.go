package gitx

import "testing"

func TestParseProgress(t *testing.T) {
	for _, tc := range []struct {
		line  string
		phase string
		frac  float64
		ok    bool
	}{
		{"Receiving objects:  45% (450/1000), 1.20 MiB | 2.00 MiB/s", "Receiving objects", .45, true},
		{"remote: Compressing objects: 100% (12/12), done.", "Compressing objects", 1, true},
		{"Resolving deltas:   0% (0/7)", "Resolving deltas", 0, true},
		{"Cloning into 'oriel'...", "", 0, false},
	} {
		phase, frac, ok := ParseProgress(tc.line)
		if phase != tc.phase || frac != tc.frac || ok != tc.ok {
			t.Errorf("%q: %q %v %v", tc.line, phase, frac, ok)
		}
	}
}
