package snapshot

import "testing"

func TestSameText(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		same bool
	}{
		{"opacity 0.19580138", "opacity 0.1958013", true},
		{"rect 563.1,181.86 1x6 fill #4f4a40", "rect 563.10004,181.86 1x6 fill #4f4a40", true},
		{"text \"Fri\" @555.4906,201.04", "text \"Fri\" @555.49066,201.04", true},
		// From macOS arm64 against Linux amd64: a size inherits the rounding of the coordinates it came from.
		{"rect 679.9733,347.18842 30.720032x3.4187927 fill #4fe0a0", "rect 679.9733,347.18845 30.720032x3.4187622 fill #4fe0a0", true},
		{"rect 1395.5951,777.7976 9.060059x44.2464 fill #ffd23f", "rect 1395.5951,777.79767 9.060059x44.246338 fill #ffd23f", true},
		{"rect 679.9733,347.18842 30.720032x3.4187927", "rect 679.9733,347.18842 30.720032x3.4197927", false},
		{"rect 1,2 3x4\nopacity 0.1958", "rect 1,2 3x4\nopacity 0.1959", false},
		{"rect 900,2 3x4\nopacity 0.1958", "rect 900,2 3x4\nopacity 0.1959", false},
		{"rect 1,2 3x4.1 fill #999999", "rect 1,2 3x4.2 fill #999999", false},
		{"rect 1,2 3x4 fill #4fe0a0", "rect 1,2 3x4 fill #4fe0a1", false},
		{"opacity 0.1958", "opacity 0.1959", false},
		{"rect 563,181 1x6", "rect 564,181 1x6", false},
		{"text \"a\" @1,2", "text \"b\" @1,2", false},
		{"rect 1,2", "rect 1,2 1x6", false},
	} {
		if got := sameText(tt.a, tt.b); got != tt.same {
			t.Errorf("sameText(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}
