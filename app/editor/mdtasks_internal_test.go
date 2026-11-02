package editor

import "testing"

// spec/gfm/spec.md, Task list items: "A [task list item marker](@) consists of an optional number of spaces, a left
// bracket (`[`), either a whitespace character or the letter `x` in either lowercase or uppercase, and then a right
// bracket (`]`)."
func TestTaskListItems(t *testing.T) {
	bs := mdParse("- [ ] write the test\n- [x] fix it\n1. [X] ship it\n- [link](https://x) stays a link")
	want := []struct{ mark, text string }{{"○", "write the test"}, {"✓", "fix it"}, {"✓", "ship it"}, {"•", "[link](https://x) stays a link"}}
	if len(bs) != len(want) {
		t.Fatalf("%d blocks: %+v", len(bs), bs)
	}
	for i, w := range want {
		if bs[i].Kind != mdItem || bs[i].Mark != w.mark || bs[i].Text != w.text {
			t.Errorf("item %d = %q %q, want %q %q", i, bs[i].Mark, bs[i].Text, w.mark, w.text)
		}
	}
}
