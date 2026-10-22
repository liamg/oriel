package app_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"testing"
)

// loadingText is wording that says a view is waiting on its data.
var loadingText = regexp.MustCompile(`(?i)^(reading|loading|asking|fetching|finding|looking (up|for)|counting|scanning|checking)\b.*…$`)

// narrations say what an agent is doing, not that a view waits on its data.
var narrations = map[string]bool{
	"agentui.assistantProgress": true, // the assistant's line for the tool it's running
}

// A view says it's loading through widget.Busy or widget.Async, so every wait looks and behaves
// the same; none draws its own "Reading…".
func TestViewsUseTheLoadingToolkit(t *testing.T) {
	files := sourceFiles(t, ".")
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || narrations[qualified(name, fn)] {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if s, err := strconv.Unquote(lit.Value); err == nil && loadingText.MatchString(s) {
					t.Errorf("%s: %s draws %q itself; use widget.Busy or widget.Async (its label without the …)", fset.Position(lit.Pos()), funcName(fn), s)
				}
				return true
			})
		}
	}
}
