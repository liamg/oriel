package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// pointerSurfaces read clicks themselves: the pointer's position is what they're about, not a control.
var pointerSurfaces = map[string]bool{
	"editorPlugin.paneView": true, // the editor's text: a click places the cursor
	"themeEditor.buildPick": true, // pick mode: a click picks whatever's under it
	"themeEditor.preview":   true, // the preview: a click picks the token under it
}

// AGENTS.md: views compose ui/widget, so nothing here reads a click off an Interaction itself; a control goes
// through widget.Clickable or a widget built on it.
func TestViewsUseWidgetsForClicks(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || pointerSurfaces[funcName(fn)] {
				continue
			}
			fromInteraction := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.AssignStmt:
					for i, r := range n.Rhs {
						if isInteractionCall(r) && i < len(n.Lhs) {
							if id, ok := n.Lhs[i].(*ast.Ident); ok {
								fromInteraction[id.Name] = true
							}
						}
					}
				case *ast.SelectorExpr:
					if n.Sel.Name != "Clicked" {
						break
					}
					id, isIdent := n.X.(*ast.Ident)
					if isInteractionCall(n.X) || isIdent && fromInteraction[id.Name] {
						t.Errorf("%s: %s reads a click off an Interaction; use widget.Clickable or a widget", fset.Position(n.Pos()), funcName(fn))
					}
				}
				return true
			})
		}
	}
}

func isInteractionCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Interaction"
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return fn.Name.Name
	}
	t := fn.Recv.List[0].Type
	if s, ok := t.(*ast.StarExpr); ok {
		t = s.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// A float's Z is a named layer (ui.LayerPage, ui.LayerOverlay + 1…), never a bare number.
func TestFloatsUseLayers(t *testing.T) {
	var files []string
	for _, dir := range []string{".", "../ui", "../ui/widget", "../ui/layout"} {
		fs, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		files = append(files, fs...)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			if k, ok := kv.Key.(*ast.Ident); !ok || k.Name != "Z" {
				return true
			}
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.INT {
				t.Errorf("%s: Z: %s; use a ui.Layer… constant", fset.Position(kv.Pos()), lit.Value)
			}
			return true
		})
	}
}
