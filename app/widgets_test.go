package app_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// pointerSurfaces read clicks themselves: the pointer's position is what they're about, not a control. Each is
// "dir.Type.func", dir its package's under app/.
var pointerSurfaces = map[string]bool{
	"editor.Editor.paneView":         true, // the editor's text: a click places the cursor
	"settings.ThemeEditor.buildPick": true, // pick mode: a click picks whatever's under it
	"settings.ThemeEditor.preview":   true, // the preview: a click picks the token under it
}

// AGENTS.md: views compose ui/widget, so nothing here reads a click off an Interaction itself; a control goes
// through widget.Clickable or a widget built on it.
func TestViewsUseWidgetsForClicks(t *testing.T) {
	files := sourceFiles(t, ".")
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || pointerSurfaces[qualified(name, fn)] {
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

// sourceFiles are the Go files, tests and testdata left out, under each of roots: "." is the app and every package
// under it (app/shell, app/kit, each app/<feature>).
func sourceFiles(t *testing.T, roots ...string) []string {
	t.Helper()
	var files []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// qualified is fn as "dir.Type.func" (or "dir.func"), dir the last element of its file's directory: "editor" for
// app/editor, "app" for the app's own.
func qualified(file string, fn *ast.FuncDecl) string {
	dir := filepath.Base(filepath.Dir(file))
	if dir == "." {
		dir = "app"
	}
	return dir + "." + funcName(fn)
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
	files := sourceFiles(t, ".", "../ui")
	fset := token.NewFileSet()
	for _, name := range files {
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
