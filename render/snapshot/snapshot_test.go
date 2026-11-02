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
