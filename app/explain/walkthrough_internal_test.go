package explain

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest/rig"
)

// A walkthrough is read out of an answer however it's wrapped, its lines written any of the ways an agent
// writes them; an answer with none is an error.
func TestParseWalkthrough(t *testing.T) {
	for _, tc := range []struct {
		name, answer string
		want         string // each step as title@start-end with its notes' spans
	}{
		{"fenced, with prose around it", rig.WalkAnswer,
			"Ties round to even@4-18[7-8 11-15]|Invoices round each line once@4-11[6-9 10-10]|The total sums rounded lines@0-0[]|Summary@0-0[]"},
		{"bare JSON", `{"steps": [{"title": "One", "file": "a.go", "start": 3, "end": 1, "prose": "p"}]}`, "One@1-3[]"},
		{"a list of steps", "```\n[{\"title\": \"One\", \"file\": \"a.go\", \"lines\": 5, \"notes\": [{\"title\": \"n\", \"lines\": \"L6–9\"}]}]\n```",
			"One@5-5[6-9]"},
		{"JSON inside prose", `Sure! {"walkthrough": [{"title": "One", "text": "body"}]} Hope that helps.`, "One@0-0[]"},
		{"empty notes and steps dropped", `{"steps": [{"title": ""}, {"title": "One", "notes": [{}]}]}`, "One@0-0[]"},
	} {
		steps, err := parseWalkthrough(tc.answer)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		var got []string
		for _, s := range steps {
			var ns []string
			for _, n := range s.Notes {
				ns = append(ns, fmt.Sprintf("%d-%d", n.Start, n.End))
			}
			got = append(got, fmt.Sprintf("%s@%d-%d[%s]", s.Title, s.Start, s.End, strings.Join(ns, " ")))
		}
		if g := strings.Join(got, "|"); g != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, g, tc.want)
		}
	}
	for _, bad := range []string{"", "No steps here.", "```json\n{\"steps\": []}\n```", "{not json"} {
		if _, err := parseWalkthrough(bad); err == nil {
			t.Errorf("%q read as a walkthrough", bad)
		}
	}
}

// The explainer reads each file's change with the file's line numbers as it is now, removed lines unnumbered,
// three lines of context, and ⋯ between far-apart changes.
func TestNumberedDiff(t *testing.T) {
	base := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\nk\nl\n"
	file := "a\nB\nc\nd\ne\nf\ng\nh\ni\nj\nl\nm\n"
	got, first, last := numberedDiff([]byte(base), []byte(file))
	want := "     1 | a\n-      | b\n+    2 | B\n     3 | c\n     4 | d\n     5 | e\n  ⋯\n     8 | h\n     9 | i\n    10 | j\n-      | k\n    11 | l\n+   12 | m\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if first != 2 || last != 12 {
		t.Errorf("changed lines %d-%d, want 2-12", first, last)
	}
}

// A step may carry a mermaid diagram, the summary most often: its source is kept as given.
func TestParseWalkthroughDiagram(t *testing.T) {
	steps, err := parseWalkthrough("```json\n" + `{"steps": [{"title": "Summary", "prose": "p", "diagram": "flowchart LR\n  A[Line] --> B[Invoice]"}]}` + "\n```")
	if err != nil || len(steps) != 1 {
		t.Fatalf("%+v %v", steps, err)
	}
	if got := steps[0].Diagram; got != "flowchart LR\n  A[Line] --> B[Invoice]" {
		t.Errorf("diagram %q", got)
	}
}
