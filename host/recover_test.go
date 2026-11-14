package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"testing"
)

// Every call the app makes on the host recovers a panic in it (docs/architecture.md): each method of the parts'
// services starts by deferring a Recover, whether it's the host's own or a part's it embeds.
func TestHostCallsRecoverPanics(t *testing.T) {
	calls := map[string]bool{}
	for _, it := range []reflect.Type{reflect.TypeFor[SessionCalls](), reflect.TypeFor[SandboxCalls](), reflect.TypeFor[ShipCalls](),
		reflect.TypeFor[CodeHostCalls](), reflect.TypeFor[RepoCalls](), reflect.TypeFor[JobCalls](), reflect.TypeFor[StatsCalls]()} {
		for i := range it.NumMethod() {
			calls[it.Method(i).Name] = true
		}
	}
	// Where each is defined: the host's own methods shadow its parts'.
	defs := map[string]*ast.FuncDecl{}
	own := map[string]bool{}
	files, _ := filepath.Glob("*.go")
	parts, _ := filepath.Glob("*/*.go")
	for _, f := range append(files, parts...) {
		if filepath.Ext(f) != ".go" || len(f) > 8 && f[len(f)-8:] == "_test.go" {
			continue
		}
		af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		host := filepath.Dir(f) == "."
		for _, d := range af.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !calls[fn.Name.Name] || fn.Body == nil {
				continue
			}
			if host && receiver(fn) == "Host" {
				defs[fn.Name.Name], own[fn.Name.Name] = fn, true
			} else if !host && !own[fn.Name.Name] && embedded[receiver(fn)] {
				defs[fn.Name.Name] = fn
			}
		}
	}
	for name := range calls {
		fn, ok := defs[name]
		if !ok {
			t.Errorf("%s: no method of the host or a part it embeds", name)
			continue
		}
		if !recovers(fn) {
			t.Errorf("%s (on %s) doesn't start by deferring a Recover", name, receiver(fn))
		}
	}
}

// embedded are the part types the host embeds, whose methods are its own.
var embedded = map[string]bool{"Logs": true, "Stats": true, "Registry": true, "Runner": true, "Boxes": true, "GitHub": true,
	"Jobs": true, "Utilities": true, "Reviews": true, "Worktrees": true}

func receiver(fn *ast.FuncDecl) string {
	t := fn.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func recovers(fn *ast.FuncDecl) bool {
	if len(fn.Body.List) == 0 {
		return false
	}
	d, ok := fn.Body.List[0].(*ast.DeferStmt)
	if !ok {
		return false
	}
	sel, ok := d.Call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Recover"
}
