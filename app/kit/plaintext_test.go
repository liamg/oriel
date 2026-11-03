package kit

import "testing"

// A comment copied "as it reads" keeps its words and lists, without markdown's marks.
func TestPlainText(t *testing.T) {
	md := "## Fixed\n\n**Cause:** the `box` id was wrong; see [the docs](https://x).\n\n- one\n- two\n\n```go\nx := 1\n```\n\n> quoted\n\n![shot](/tmp/a.png) #123 stays"
	want := "Fixed\n\nCause: the box id was wrong; see the docs.\n\n- one\n- two\n\nx := 1\n\nquoted\n\n#123 stays"
	if got := PlainText(md); got != want {
		t.Errorf("PlainText =\n%q\nwant\n%q", got, want)
	}
}
