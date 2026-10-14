package testrun

import (
	"strings"
	"testing"
)

// TestWantGotTestify reads testify's assert.Equal failure, as its format string lays it out.
//
// ref: testify/assert/assertions.go#Equal
// quote: "return Fail(t, fmt.Sprintf(\"Not equal: \\n\"+ \"expected: %s\\n\"+ \"actual  : %s%s\", expected, actual, diff), msgAndArgs...)"
func TestWantGotTestify(t *testing.T) {
	out := "    calc_test.go:12: \n" +
		"        \tError Trace:\t/src/calc_test.go:12\n" +
		"        \tError:      \tNot equal: \n" +
		"        \t            \texpected: 5\n" +
		"        \t            \tactual  : 4\n" +
		"        \tTest:       \tTestAdd\n"
	f := goFailure("TestAdd", strings.Split(strings.TrimSuffix(out, "\n"), "\n"))
	if f.Want != "5" || f.Got != "4" || f.Loc == nil || f.Loc.String() != "calc_test.go:12" {
		t.Errorf("failure = %+v loc %v", f, f.Loc)
	}
}

func TestWantGotShapes(t *testing.T) {
	for _, c := range []struct {
		lines     []string
		want, got string
	}{
		{[]string{"Add(2, 2) = 4, want 5"}, "5", "4"},
		{[]string{"got: [1 2], want: [1 3]"}, "[1 3]", "[1 2]"},
		{[]string{`wanted "a", got "b"`}, `"a"`, `"b"`},
		{[]string{"assert 4 == 5"}, "5", "4"},
		{[]string{"AssertionError: expected 4 to be 5 // Object.is equality"}, "5", "4"},
		{[]string{"Expected: 5", "Received: 4"}, "5", "4"},
		{[]string{"assertion `left == right` failed", "  left: 4", " right: 5"}, "5", "4"},
		{[]string{"nothing to see"}, "", ""},
		{[]string{"AssertionError: expected 'a' not to be 'a'"}, "", ""},
	} {
		f := &Failure{}
		wantGot(f, c.lines)
		if f.Want != c.want || f.Got != c.got {
			t.Errorf("%q: want %q got %q, expected %q %q", c.lines, f.Want, f.Got, c.want, c.got)
		}
	}
}

func TestFileLine(t *testing.T) {
	for in, want := range map[string]string{
		"src/lib.rs:26:9": "src/lib.rs:26",
		"/a/b_test.go:12": "/a/b_test.go:12",
		"C:/x/y.ts:3:1":   "C:/x/y.ts:3",
		"no line here":    "",
		"tests/x.py:0":    "",
	} {
		got := ""
		if l, ok := fileLine(in); ok {
			got = l.String()
		}
		if got != want {
			t.Errorf("fileLine(%q) = %q, want %q", in, got, want)
		}
	}
}
