package ratchet

import (
	"go/ast"
	"strings"
	"testing"
)

// A test that measures the machine is a …Budget behind the perf tag, and every test there is one, so make perf
// (-tags perf -run 'Budget$') runs them all and go test ./... none.
func TestBudgetsAreBehindThePerfTag(t *testing.T) {
	files, err := ParseAll(repoRoot(t), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range without(tests(files), "vendor/", "third_party/") {
		perf := buildTag(f, "perf")
		for _, d := range f.AST.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" {
				continue
			}
			switch budget := strings.HasSuffix(fn.Name.Name, "Budget"); {
			case budget && !perf:
				t.Errorf("%s: %s is a budget outside a perf-tagged file", pos(f, fn), fn.Name.Name)
			case perf && !budget:
				t.Errorf("%s: %s is in a perf-tagged file but not named …Budget, so make perf skips it", pos(f, fn), fn.Name.Name)
			}
		}
	}
}
