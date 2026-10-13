package syntax

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitRootPatterns(t *testing.T) {
	patterns := []string{
		"(comment) @comment",
		"(source_file\n  .\n  (comment)+ @comment.documentation)",
		"(source_file\n  (comment)+ @comment.documentation\n  .\n  (function_declaration))",
		`"func" @keyword`,
		`((identifier) @x (#eq? @x "(source_file"))`,
		"((source_file . (comment) @shebang) (#lua-match? @shebang \"^#!\"))",
	}
	main, rest, mo, ro := splitRootPatterns(patterns, "source_file")
	if !slices.Equal(mo, []int{0, 2, 3, 4}) || !slices.Equal(ro, []int{1, 5}) {
		t.Errorf("orders %v %v", mo, ro)
	}
	for _, want := range []string{"(comment) @comment", "((comment)+ @comment.documentation\n  .\n  (function_declaration) (#has-parent? @comment.documentation source_file))",
		`"func" @keyword`, `((identifier) @x (#eq? @x "(source_file"))`} {
		if !strings.Contains(main, want) {
			t.Errorf("main lacks %q:\n%s", want, main)
		}
	}
	if !strings.Contains(rest, "@shebang") || !strings.Contains(rest, "(source_file\n  .\n  (comment)+ @comment.documentation)") {
		t.Errorf("rest: %q", rest)
	}
}
