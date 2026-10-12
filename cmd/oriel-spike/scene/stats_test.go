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

func TestSplits(t *testing.T) {
	if s := splits(10, 5, 10); len(s) != 1 || s[0] != (span{0, 10, 5, 10}) {
		t.Errorf("unstretched: %v", s)
	}
	s := splits(9, 0, 100)
	if s[0] != (span{0, 4, 0, 4}) || s[1] != (span{4, 1, 4, 92}) || s[2] != (span{5, 4, 96, 4}) {
		t.Errorf("stretched: %v", s)
	}
}

func TestGoFile(t *testing.T) {
	f := GoFile(FileLines)
	if len(f) != FileLines || f[0] != "package pricing" {
		t.Fatalf("got %d lines, first %q", len(f), f[0])
	}
}
