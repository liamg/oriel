package testhook

import "testing"

var knob = 1

func TestSetRestores(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		Set(t, &knob, 2)
		if knob != 2 {
			t.Fatalf("knob %d", knob)
		}
	})
	if knob != 1 {
		t.Fatalf("knob %d after the test", knob)
	}
}
