package voice

import "testing"

func TestMeteredValue(t *testing.T) {
	for out, want := range map[string]bool{"u 1\n": true, "u 3\n": true, "u 4\n": false, "(<uint32 1>,)\n": true, "(<uint32 2>,)": false, "": false} {
		if got := meteredValue(out); got != want {
			t.Errorf("%q: %v", out, got)
		}
	}
}
