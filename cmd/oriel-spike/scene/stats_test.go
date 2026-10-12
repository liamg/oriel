package scene

import (
	"testing"
	"time"
)

func TestPct(t *testing.T) {
	var d Durations
	for i := 1; i <= 100; i++ {
		d = append(d, time.Duration(101-i)*time.Millisecond)
	}
	for _, c := range []struct {
		p    float64
		want time.Duration
	}{{50, 50 * time.Millisecond}, {99, 99 * time.Millisecond}, {100, 100 * time.Millisecond}, {0, time.Millisecond}} {
		if got := d.Pct(c.p); got != c.want {
			t.Errorf("Pct(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if (Durations{}).Pct(50) != 0 {
		t.Error("empty Pct should be 0")
	}
}

func TestGoFile(t *testing.T) {
	f := GoFile(FileLines)
	if len(f) != FileLines || f[0] != "package pricing" {
		t.Fatalf("got %d lines, first %q", len(f), f[0])
	}
}
