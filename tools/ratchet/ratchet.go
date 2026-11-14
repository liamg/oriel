// Package ratchet counts patterns the code is moving away from, so a test can hold each count to a ceiling that
// only falls.
package ratchet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// File is one parsed non-test Go file, by its slash path from the repo root.
type File struct {
	Path string
	Fset *token.FileSet
	AST  *ast.File
	Src  []byte
}

// Parse reads every non-test Go file under root's dirs, skipping vendor and testdata.
func Parse(root string, dirs ...string) ([]File, error) { return parse(root, false, dirs) }

// ParseAll reads every Go file under root's dirs, tests too, skipping vendor and testdata.
func ParseAll(root string, dirs ...string) ([]File, error) { return parse(root, true, dirs) }

// parse reads the Go files under root's dirs, tests among them when tests is set.
func parse(root string, tests bool, dirs []string) ([]File, error) {
	var out []File
	for _, d := range dirs {
		err := filepath.WalkDir(filepath.Join(root, d), func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if e.IsDir() {
				if n := e.Name(); n == "vendor" || n == "testdata" || (strings.HasPrefix(n, ".") && p != filepath.Join(root, d)) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".go") || !tests && strings.HasSuffix(p, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution|parser.ParseComments)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			out = append(out, File{Path: filepath.ToSlash(rel), Fset: fset, AST: f})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Hit is one counted occurrence.
type Hit struct {
	Pos  string
	What string
}

func pos(f File, n ast.Node) string { return f.Fset.Position(n.Pos()).String() }

// ownKinds are the kinds a file says it implements, with a line "//oriel:kind review" among its comments: a kind's
// own code may name its kind and read its data.
func ownKinds(f File) map[string]bool {
	out := map[string]bool{}
	for _, g := range f.AST.Comments {
		for _, c := range g.List {
			if k, ok := strings.CutPrefix(c.Text, "//oriel:kind "); ok {
				out[strings.TrimSpace(k)] = true
			}
		}
	}
	return out
}

// kindConst is the kind core.KindX names ("review" for core.KindReview), if e is one.
func kindConst(e ast.Expr) (string, bool) {
	s, ok := e.(*ast.SelectorExpr)
	if !ok || !strings.HasPrefix(s.Sel.Name, "Kind") || len(s.Sel.Name) == len("Kind") {
		return "", false
	}
	id, ok := s.X.(*ast.Ident)
	return strings.ToLower(strings.TrimPrefix(s.Sel.Name, "Kind")), ok && id.Name == "core"
}

// KindCompares finds core.Kind* constants compared with == or != or used as switch cases, outside the files of the
// kind they name (ownKinds).
func KindCompares(files []File) []Hit {
	var hits []Hit
	for _, f := range files {
		own := ownKinds(f)
		isKind := func(e ast.Expr) bool {
			k, ok := kindConst(e)
			return ok && !own[k]
		}
		ast.Inspect(f.AST, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BinaryExpr:
				if (n.Op == token.EQL || n.Op == token.NEQ) && (isKind(n.X) || isKind(n.Y)) {
					hits = append(hits, Hit{pos(f, n), "kind compared"})
				}
			case *ast.CaseClause:
				for _, e := range n.List {
					if isKind(e) {
						hits = append(hits, Hit{pos(f, e), "kind as a case"})
					}
				}
			}
			return true
		})
	}
	return hits
}

// KindStandIns finds a kind's own data read outside the files of that kind (ownKinds): core.DataOf[core.ReviewData]
// or se.Data(core.KindReview). A session keeps its kind's data there, not in fields of its own (TestNoKindFields), so
// reading it elsewhere is testing for the kind; the rest ask its kind (Can, Label, Chips) or call the kind's own
// helpers.
func KindStandIns(files []File) []Hit {
	var hits []Hit
	for _, f := range files {
		own := ownKinds(f)
		ast.Inspect(f.AST, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fn := c.Fun.(type) {
			case *ast.IndexExpr: // core.DataOf[core.ReviewData](se)
				s, ok := fn.X.(*ast.SelectorExpr)
				t, ok2 := fn.Index.(*ast.SelectorExpr)
				if ok && ok2 && s.Sel.Name == "DataOf" {
					if k := strings.ToLower(strings.TrimSuffix(t.Sel.Name, "Data")); !own[k] {
						hits = append(hits, Hit{pos(f, c), "kind data read"})
					}
				}
			case *ast.SelectorExpr: // se.Data(core.KindReview)
				if fn.Sel.Name == "Data" && len(c.Args) == 1 {
					if k, ok := kindConst(c.Args[0]); ok && !own[k] {
						hits = append(hits, Hit{pos(f, c), "kind data read"})
					}
				}
			}
			return true
		})
	}
	return hits
}

// Selectors finds uses of a selector chain, such as "cfg.Store" for x.cfg.Store.
func Selectors(files []File, chain string) []Hit {
	parts := strings.Split(chain, ".")
	var hits []Hit
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			s, ok := n.(*ast.SelectorExpr)
			if !ok || s.Sel.Name != parts[len(parts)-1] {
				return true
			}
			e := s.X
			for i := len(parts) - 2; i >= 0; i-- {
				x, ok := e.(*ast.SelectorExpr)
				if !ok || x.Sel.Name != parts[i] {
					return true
				}
				e = x.X
			}
			hits = append(hits, Hit{pos(f, s), chain})
			return true
		})
	}
	return hits
}

// PkgSelectors finds pkg.name: a package's function or var, named through the package as the file imports it.
func PkgSelectors(files []File, pkg, name string) []Hit {
	var hits []Hit
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == name {
				if id, ok := s.X.(*ast.Ident); ok && id.Name == pkg {
					hits = append(hits, Hit{pos(f, s), pkg + "." + name})
				}
			}
			return true
		})
	}
	return hits
}

// TypeRefs finds *name used as a type.
func TypeRefs(files []File, name string) []Hit {
	var hits []Hit
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if s, ok := n.(*ast.StarExpr); ok {
				if id, ok := s.X.(*ast.Ident); ok && id.Name == name {
					hits = append(hits, Hit{pos(f, s), "*" + name})
				}
			}
			return true
		})
	}
	return hits
}

// Calls finds calls of a method or function named name, on any receiver.
func Calls(files []File, name string) []Hit {
	var hits []Hit
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == name {
					hits = append(hits, Hit{pos(f, c), name})
				}
			}
			return true
		})
	}
	return hits
}

// UICalls finds calls with one argument of a method named one of names, in files that import the ui package: the
// toolkit's Ctx.Focus, not a store's Focus().
func UICalls(files []File, names ...string) []Hit {
	var hits []Hit
	for _, f := range files {
		if !imports(f, "github.com/liamg/oriel/ui") {
			continue
		}
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok && len(c.Args) == 1 {
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && slices.Contains(names, s.Sel.Name) {
					hits = append(hits, Hit{pos(f, c), s.Sel.Name})
				}
			}
			return true
		})
	}
	return hits
}

func imports(f File, path string) bool {
	for _, im := range f.AST.Imports {
		if strings.Trim(im.Path.Value, `"`) == path {
			return true
		}
	}
	return false
}

// Multiline finds widget.InputOpts literals that set Lines: a multi-line Input, where a widget.Prose belongs.
func Multiline(files []File) []Hit {
	var hits []Hit
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			l, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if s, ok := l.Type.(*ast.SelectorExpr); !ok || s.Sel.Name != "InputOpts" {
				return true
			}
			for _, e := range l.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Lines" {
					if b, ok := kv.Value.(*ast.BasicLit); !ok || (b.Value != "0" && b.Value != "1") {
						hits = append(hits, Hit{pos(f, l), "multi-line Input"})
					}
				}
			}
			return true
		})
	}
	return hits
}

// LongFuncs finds functions with more than max lines.
func LongFuncs(files []File, max int) []Hit {
	var hits []Hit
	for _, f := range files {
		for _, d := range f.AST.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if n := f.Fset.Position(fn.End()).Line - f.Fset.Position(fn.Pos()).Line + 1; n > max {
				hits = append(hits, Hit{pos(f, fn), fn.Name.Name})
			}
		}
	}
	return hits
}

// LongFiles finds files with more than max lines.
func LongFiles(files []File, max int) []Hit {
	var hits []Hit
	for _, f := range files {
		if n := f.Fset.Position(f.AST.FileEnd).Line; n > max {
			hits = append(hits, Hit{f.Path, "long file"})
		}
	}
	return hits
}
