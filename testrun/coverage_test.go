package testrun

import (
	"slices"
	"testing"
)

func TestCoverageModel(t *testing.T) {
	c := &Coverage{}
	for line, hits := range map[int]int{0: 1, 1: 2, 3: 0, 4: 0, 6: 5} {
		c.Hit("a.go", line, hits)
	}
	c.Hit("a.go", 3, 0)
	want := []LineRange{{0, 1, true}, {3, 4, false}, {6, 6, true}}
	if got := c.Ranges("a.go"); !slices.Equal(got, want) {
		t.Errorf("ranges = %+v, want %+v", got, want)
	}
	o := &Coverage{}
	o.Hit("a.go", 3, 1)
	o.Hit("b.go", 0, 0)
	c.Merge(o)
	if c.Files["a.go"][3] != 1 || c.Files["b.go"][0] != 0 {
		t.Errorf("merged = %v", c.Files)
	}
	if p := c.Percent("a.go"); p != 80 {
		t.Errorf("a.go %.1f%%, want 80", p)
	}
	if p := c.Percent(""); p != 100*4.0/6 {
		t.Errorf("all %.2f%%", p)
	}
}
