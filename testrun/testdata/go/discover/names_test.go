package discover

import "testing"

func Test(t *testing.T) {}

func Testlower(t *testing.T) {}

func TestÉclair(t *testing.T) {}

func TestMain(m *testing.M) {}

func helper(t *testing.T) {}

func TestRegexChars(t *testing.T) {
	t.Run("a+b (c)", func(t *testing.T) {})
	t.Run(`raw\name`, func(t *testing.T) {})
}

func TestPositional(t *testing.T) {
	for _, tc := range []struct {
		in   int
		name string
	}{
		{1, "one"},
		{2, "two"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("inside a row", func(t *testing.T) {})
		})
	}
}

func TestComputedName(t *testing.T) {
	cases := []struct{ name string }{{name: "kept"}, {name: fmt.Sprint(1)}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {})
	}
}
