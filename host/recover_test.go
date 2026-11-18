package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liamg/oriel/host/hostapi"
)

// Every call the app makes on the host recovers a panic in it (docs/architecture.md): each method of the parts'
// services starts by deferring a Recover, whether it's the host's own or, for one the host hands to a part (api.go),
// the part's.
func TestHostCallsRecoverPanics(t *testing.T) {
	calls := map[string]bool{}
	for _, it := range []reflect.Type{reflect.TypeFor[hostapi.SessionCalls](), reflect.TypeFor[hostapi.SandboxCalls](), reflect.TypeFor[hostapi.ShipCalls](),
		reflect.TypeFor[hostapi.CodeHostCalls](), reflect.TypeFor[hostapi.RepoCalls](), reflect.TypeFor[hostapi.JobCalls](), reflect.TypeFor[hostapi.StatsCalls]()} {
		for i := range it.NumMethod() {
			calls[it.Method(i).Name] = true
		}
	}
	own := map[string]*ast.FuncDecl{}        // the host's methods
	partDefs := map[string][]*ast.FuncDecl{} // the parts' methods, by name
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
			switch {
			case host && receiver(fn) == "Host":
				own[fn.Name.Name] = fn
			case !host && partTypes[receiver(fn)]:
				partDefs[fn.Name.Name] = append(partDefs[fn.Name.Name], fn)
			}
		}
	}
	for name := range calls {
		fn, ok := own[name]
		switch {
		case !ok:
			t.Errorf("%s: no method of the host", name)
		case recovers(fn):
		case !handsOn(fn):
			t.Errorf("%s (on Host) doesn't start by deferring a Recover", name)
		case len(partDefs[name]) == 0:
			t.Errorf("%s: Host hands it on, but no part has it", name)
		default:
			for _, pf := range partDefs[name] {
				if !recovers(pf) {
					t.Errorf("%s (on %s, which Host hands it to) doesn't start by deferring a Recover", name, receiver(pf))
				}
			}
		}
	}
}

// handsOn says fn only calls the method of the same name on one of the host's parts: w.part.Name(...).
func handsOn(fn *ast.FuncDecl) bool {
	if len(fn.Body.List) != 1 {
		return false
	}
	var call ast.Expr
	switch s := fn.Body.List[0].(type) {
	case *ast.ReturnStmt:
		if len(s.Results) != 1 {
			return false
		}
		call = s.Results[0]
	case *ast.ExprStmt:
		call = s.X
	}
	c, ok := call.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != fn.Name.Name {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	w, ok := inner.X.(*ast.Ident)
	return ok && w.Name == "w"
}

// partTypes are the host's parts.
var partTypes = map[string]bool{"Logs": true, "Stats": true, "Registry": true, "Runner": true, "Boxes": true, "Ships": true,
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
