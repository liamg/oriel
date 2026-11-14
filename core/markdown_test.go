package core

import "testing"

// A thread's heading is shown unrendered, so the markup a rendered comment hides (badges, HTML comments and tags)
// stays out of it and the first line with text is the heading.
func TestHeadingSkipsMarkup(t *testing.T) {
	for body, want := range map[string]string{
		"<!-- linear-linkback -->\nOriel hid the linear[bot] comment":                   "Oriel hid the linear[bot] comment",
		"![medium](https://img.shields.io/badge/severity-medium-yellow) Owen is right":  "Owen is right",
		"[![low](https://img.shields.io/badge/x.svg)](https://x)\n\n**Verdict:** close": "Verdict: close",
		"<details><summary>Why</summary>\nbecause</details>":                            "Why",
		"## The rule `lone task`": "The rule lone task",
		"a < b and c > d":         "a < b and c > d",
		"<!-- only a marker -->":  "t1",
	} {
		th := Thread{ID: "t1", Comments: []Comment{{Body: body}}}
		if got := th.Heading(); got != want {
			t.Errorf("Heading(%q) = %q, want %q", body, got, want)
		}
	}
}

// Emphasis keeps its words; an underscore in a name and a lone star aren't emphasis.
func TestPlainMarkdownEmphasis(t *testing.T) {
	for in, want := range map[string]string{
		"an *important* point":    "an important point",
		"_really_ do it":          "really do it",
		"set max_age and min_age": "set max_age and min_age",
		"2 * 3 = 6":               "2 * 3 = 6",
		"**bold** and *em*":       "bold and em",
	} {
		if got := PlainMarkdown(in); got != want {
			t.Errorf("PlainMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}
