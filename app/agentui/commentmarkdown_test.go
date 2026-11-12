package agentui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// commentScene is a session with one thread on round.go's line 4 whose comment is body, shown between the editor's
// lines (where = "inline") or open as its request (where = "request").
func commentScene(t *testing.T, where, body string) *apptest.Scene {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "round.go"), []byte("package pricing\n\nfunc Round() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	s := apptest.ThreadsScene(t, apptest.Options{WorkDir: dir}, [4]string{"round.go", "4", "\tfloor := scaled.Floor()", body})
	if where == "request" {
		s.Run("request.open", registry.Args{"thread": "t000000"})
	} else {
		s.Run("editor.open", registry.Args{"path": "round.go"})
	}
	s.Frames(3)
	return s
}

// runsWith are the frame's runs of text holding any of words.
func runsWith(s *apptest.Scene, words ...string) []render.ShownRun {
	var out []render.ShownRun
	for _, r := range s.List().Runs() {
		for _, w := range words {
			if strings.Contains(r.Text, w) {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// A comment's code wraps rather than running out of its box: a fence's long line (a fence in a list item too) and a
// long inline code span each break onto more rows, every row of them shown whole.
//
// ref:   gfm/spec.md#List items
// quote: "indenting subsequent lines of *Ls* by *W + N* spaces, is a list item with *Bs* as its contents."
// ref:   gfm/spec.md#Fenced code blocks
// quote: "If the leading code fence is indented N spaces, then up to N spaces of indentation are removed from each line of the content (if present)."
func TestCommentCodeWraps(t *testing.T) {
	fence := "fenceA fenceB " + strings.Repeat("roundHalfEven(scaled) ", 8) + "fenceEND"
	nested := "nestA nestB " + strings.Repeat("floor.Truncate(sign) ", 8) + "nestEND"
	inline := "spanA" + strings.Repeat("RoundingModeHalfEven", 8) + "spanEND"
	body := "Two ways round:\n\n```go\n" + fence + "\n```\n\n1. Truncate it:\n   ```go\n   " + nested + "\n   ```\n2. Or call `" + inline + "` instead."
	for _, where := range []string{"inline", "request"} {
		t.Run(where, func(t *testing.T) {
			s := commentScene(t, where, body)
			for _, c := range []struct{ name, first, last string }{{"the fence", "fenceA", "fenceEND"}, {"the list item's fence", "nestA", "nestEND"},
				{"the code span", "spanA", "spanEND"}} {
				runs := runsWith(s, c.first, c.last, "roundHalfEven", "floor.Truncate", "RoundingModeHalfEven")
				var rows []render.ShownRun
				for _, r := range runs {
					if strings.Contains(r.Text, c.first) || len(rows) > 0 {
						rows = append(rows, r)
					}
					if strings.Contains(r.Text, c.last) {
						break
					}
				}
				if len(rows) < 2 || !strings.Contains(rows[len(rows)-1].Text, c.last) {
					t.Errorf("%s doesn't wrap onto rows ending in %q: %+v\n%s", c.name, c.last, rows, s.Text())
					continue
				}
				for _, r := range rows {
					if r.Shown != r.Box || strings.HasSuffix(r.Text, "…") || strings.Contains(r.Text, "```") {
						t.Errorf("%s's row %q is cut: box %v, shown %v", c.name, r.Text, r.Box, r.Shown)
					}
				}
			}
		})
	}
}

// A comment keeps the lines it was written in, as GitHub's comments do: a newline is a line break (a blank line still
// starts a paragraph), in a paragraph and a list item alike.
//
// ref:   github-docs/basic-writing-and-formatting-syntax.md#Line breaks
// quote: "If you're writing in issues, pull requests, or discussions in a repository, {% data variables.product.github %} will render a line break automatically:"
// ref:   gfm/spec.md#Soft line breaks
// quote: "A renderer may also provide an option to render soft line breaks as hard line breaks."
func TestCommentKeepsItsLineBreaks(t *testing.T) {
	body := "Floor goes the wrong way.\nAnd for zero?\n\nTwo cases:\n- negatives\n  and zero\n- the rest"
	for _, where := range []string{"inline", "request"} {
		t.Run(where, func(t *testing.T) {
			s := commentScene(t, where, body)
			if text := s.Text(); !apptest.HasAll(text, "\nFloor goes the wrong way.\nAnd for zero?\n", "\nTwo cases:\n", "\nnegatives\nand zero\n", "\nthe rest\n") {
				t.Errorf("the comment's lines are joined:\n%s", text)
			}
			first, second := runsWith(s, "Floor goes the wrong way."), runsWith(s, "And for zero?")
			if len(first) == 0 || len(second) != 1 {
				t.Fatalf("the comment's lines aren't drawn: %+v, %+v", first, second)
			}
			if a, b := first[len(first)-1], second[0]; b.At.Y <= a.At.Y || b.At.X != a.At.X {
				t.Errorf("the second line isn't under the first: %+v, %+v", a, b)
			}
		})
	}
}
