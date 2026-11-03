package calc

import (
	"fmt"
	"testing"
)

func TestPass(t *testing.T) {
	if got := Add(1, 2); got != 3 {
		t.Errorf("Add(1, 2) = %d, want 3", got)
	}
}

func TestGotWant(t *testing.T) {
	got := Add(2, 2)
	t.Errorf("Add(2, 2): got %d, want %d", got, 5)
}

func TestWantGot(t *testing.T) {
	t.Fatalf("want %q, got %q", "five", fmt.Sprint(Add(2, 2)))
}

func TestSkip(t *testing.T) {
	t.Skip("not today")
}

func TestLogs(t *testing.T) {
	t.Log("first line")
	fmt.Println("printed to stdout")
}

func TestNested(t *testing.T) {
	t.Run("outer", func(t *testing.T) {
		t.Run("inner pass", func(t *testing.T) {})
		t.Run("inner fail", func(t *testing.T) {
			t.Error("got 1, want 2")
		})
	})
}

func TestParallel(t *testing.T) {
	t.Run("a", func(t *testing.T) {
		t.Parallel()
	})
	t.Run("b", func(t *testing.T) {
		t.Parallel()
		t.Error("b failed")
	})
}

func TestTable(t *testing.T) {
	tests := []struct {
		name       string
		a, b, want int
	}{
		{"one plus one", 1, 1, 2},
		{name: "two plus two", a: 2, b: 2, want: 5},
		{
			name: "zero",
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Add(tc.a, tc.b); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMapTable(t *testing.T) {
	cases := map[string]struct{ a, b, want int }{
		"ten/two":   {10, 2, 5},
		"six/three": {6, 3, 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := Div(tc.a, tc.b); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDuplicate(t *testing.T) {
	t.Run("same", func(t *testing.T) {})
	t.Run("same", func(t *testing.T) {})
	t.Run("", func(t *testing.T) {})
}

func helper() {}

func TestMain_ok(t *testing.T) {}
