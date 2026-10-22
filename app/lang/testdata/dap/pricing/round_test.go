package pricing

import "testing"

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		name string
		v    float64
		p    int
		want float64
	}{
		{"2.5", 2.5, 0, 2},
		{"3.5", 3.5, 0, 4},
		{"-2.5", -2.5, 0, -2},
		{"-3.5", -3.5, 0, -4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := roundHalfEven(c.v, c.p); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
