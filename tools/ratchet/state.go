package ratchet

import (
	"go/ast"
	"go/token"
	"path"
	"slices"
	"strings"
	"unicode"
)

// Package-level state: a package-level var that holds mutable state is shared by every app, window and host in the
// process, and by every test in a test binary, so one leaks into the next. State belongs to the instance that owns
// it (the app, a plugin, the host, a client) and is released when that closes. A var counts as state when its type
// or value is a lock, an atomic, a channel or a pointer to something made, or when code other than its declaration
// and init writes to it. Constant tables, compiled regexps, sentinel errors, theme tokens and embedded files don't.

// pureCalls make values nothing writes to later.
var pureCalls = []string{
	"regexp.MustCompile", "errors.New", "fmt.Errorf", "theme.Declare", "theme.DeclareOn", "theme.semanticToken",
	"theme.semanticOn", "strings.NewReplacer",
	"template.Must", "time.Date", "time.Duration", "json.RawMessage", "slices.Clone", "append", "float32", "reflect.TypeFor",
	"strings.Replace", "strings.ReplaceAll", "strings.Repeat", "strings.Join", "fmt.Sprintf", "filepath.Join",
}

func isTest(f File) bool { return strings.HasSuffix(f.Path, "_test.go") }

// pkgVar is a package-level var: its package's directory and name.
type pkgVar struct{ dir, name string }

// packageVars are each directory's package-level vars and their specs, of its non-test files (tests too with tests).
func packageVars(files []File, tests bool) map[pkgVar]*ast.ValueSpec {
	out := map[pkgVar]*ast.ValueSpec{}
	for _, f := range files {
		if isTest(f) && !tests {
			continue
		}
		for _, d := range f.AST.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, s := range g.Specs {
				vs := s.(*ast.ValueSpec)
				for _, n := range vs.Names {
					if n.Name != "_" {
						out[pkgVar{path.Dir(f.Path), n.Name}] = vs
					}
				}
			}
		}
	}
	return out
}

// MutableVars finds package-level vars holding mutable state in files' packages (the rule above), but for the
// allowed, by "dir.name".
func MutableVars(files []File, module string, allowed map[string]string) []Hit {
	vars := packageVars(files, false)
	written := map[pkgVar]bool{}
	for _, f := range files {
		if isTest(f) {
			continue
		}
		for _, w := range writes(f, vars, module, true) {
			written[w.v] = true
		}
	}
	var hits []Hit
	for _, f := range files {
		if isTest(f) {
			continue
		}
		for _, d := range f.AST.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.VAR {
				continue
			}
			for _, s := range g.Specs {
				vs := s.(*ast.ValueSpec)
				if embedded(g, vs) {
					continue
				}
				for i, n := range vs.Names {
					v := pkgVar{path.Dir(f.Path), n.Name}
					if n.Name == "_" || allowed[v.dir+"."+v.name] != "" {
						continue
					}
					var val ast.Expr
					if i < len(vs.Values) {
						val = vs.Values[i]
					} else if len(vs.Values) == 1 {
						val = vs.Values[0]
					}
					switch {
					case val == nil && named(vs.Type):
						hits = append(hits, Hit{pos(f, n), n.Name + " is a " + typeName(vs.Type) + " for its methods to change"})
					case written[v]:
						hits = append(hits, Hit{pos(f, n), n.Name + " written after it's declared"})
					case sentinel(n.Name):
						// a sentinel error, made once: a write to it is caught above
					case stateful(f.AST.Name.Name, vs.Type) || stateful(f.AST.Name.Name, val):
						hits = append(hits, Hit{pos(f, n), n.Name + " holds a lock, atomic, channel or pointer"})
					}
				}
			}
		}
	}
	return hits
}

func embedded(g *ast.GenDecl, vs *ast.ValueSpec) bool {
	for _, d := range []*ast.CommentGroup{g.Doc, vs.Doc} {
		if d != nil && strings.Contains(d.Text(), "go:embed") {
			return true
		}
		if d != nil {
			for _, c := range d.List {
				if strings.HasPrefix(c.Text, "//go:embed") {
					return true
				}
			}
		}
	}
	return false
}

// sentinel says name is a sentinel error's, by Go's naming: errX, ErrX.
func sentinel(name string) bool {
	rest, ok := strings.CutPrefix(name, "err")
	if !ok {
		rest, ok = strings.CutPrefix(name, "Err")
	}
	return ok && rest != "" && unicode.IsUpper(rune(rest[0]))
}

// stateful says e's type or value is a lock, an atomic, a channel, or a pointer to something made. pkg is the
// package e is in, so a call to one of its own pure functions is known as the qualified name pureCalls lists.
func stateful(pkg string, e ast.Expr) bool {
	if e == nil {
		return false
	}
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.SelectorExpr:
			if id, ok := n.X.(*ast.Ident); ok && (id.Name == "sync" || id.Name == "atomic") {
				found = true
			}
		case *ast.ChanType:
			found = true
		case *ast.UnaryExpr:
			if n.Op == token.AND {
				found = true
			}
		case *ast.CallExpr:
			name := callName(n.Fun)
			switch {
			case name == "make" || name == "new":
				found = true
			case !slices.Contains(pureCalls, name) && !slices.Contains(pureCalls, pkg+"."+name) && !conversion(n):
				found = true
			}
		}
		return !found
	})
	return found
}

var basic = map[string]bool{"int": true, "int64": true, "uint32": true, "float32": true, "float64": true, "string": true, "byte": true, "rune": true}

func callName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name + "." + e.Sel.Name
		}
	case *ast.IndexExpr:
		return callName(e.X)
	}
	return ""
}

// conversion says c converts a value to a basic type, or calls a func literal at once to build a table.
func conversion(c *ast.CallExpr) bool {
	if _, ok := c.Fun.(*ast.FuncLit); ok && len(c.Args) == 0 {
		return true
	}
	if len(c.Args) != 1 {
		return false
	}
	id, ok := c.Fun.(*ast.Ident)
	return ok && basic[id.Name]
}

type write struct {
	v   pkgVar
	pos string
}

// writes are f's writes to vars: assignments, ++ and --, delete and clear, outside testhook.Set; in product code,
// &v too, and outside init. A name its function declares itself is that function's own.
func writes(f File, vars map[pkgVar]*ast.ValueSpec, module string, product bool) []write {
	dir := path.Dir(f.Path)
	imports := map[string]string{} // local name → dir
	for _, im := range f.AST.Imports {
		p := strings.Trim(im.Path.Value, `"`)
		rel, ok := strings.CutPrefix(p, module+"/")
		if !ok {
			continue
		}
		name := path.Base(rel)
		if im.Name != nil {
			name = im.Name.Name
		}
		imports[name] = rel
	}
	// an external test package's own dir is its package's
	var out []write
	for _, d := range f.AST.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil || (product && fn.Recv == nil && fn.Name.Name == "init") {
			continue
		}
		locals := declared(fn)
		target := func(e ast.Expr) (pkgVar, bool) {
			for {
				switch x := e.(type) {
				case *ast.IndexExpr:
					e = x.X
					continue
				case *ast.ParenExpr:
					e = x.X
					continue
				case *ast.StarExpr:
					e = x.X
					continue
				case *ast.SelectorExpr:
					if id, ok := x.X.(*ast.Ident); ok {
						if pd, ok := imports[id.Name]; ok && !locals[id.Name] {
							v := pkgVar{pd, x.Sel.Name}
							_, ok := vars[v]
							return v, ok
						}
					}
					e = x.X
					continue
				case *ast.Ident:
					if locals[x.Name] {
						return pkgVar{}, false
					}
					v := pkgVar{dir, x.Name}
					_, ok := vars[v]
					return v, ok
				}
				return pkgVar{}, false
			}
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				if name := callName(n.Fun); name == "testhook.Set" {
					return false
				} else if (name == "delete" || name == "clear") && len(n.Args) > 0 {
					if v, ok := target(n.Args[0]); ok {
						out = append(out, write{v, pos(f, n)})
					}
				}
			case *ast.AssignStmt:
				if n.Tok == token.DEFINE {
					return true
				}
				for _, l := range n.Lhs {
					if v, ok := target(l); ok {
						out = append(out, write{v, pos(f, n)})
					}
				}
			case *ast.IncDecStmt:
				if v, ok := target(n.X); ok {
					out = append(out, write{v, pos(f, n)})
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND && product { // a test's &v is a table's entry, read
					if v, ok := target(n.X); ok {
						out = append(out, write{v, pos(f, n)})
					}
				}
			}
			return true
		})
	}
	return out
}

// declared are the names fn declares: its receiver, parameters and results, and its := and var.
func declared(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	fields := func(l *ast.FieldList) {
		if l == nil {
			return
		}
		for _, f := range l.List {
			for _, n := range f.Names {
				out[n.Name] = true
			}
		}
	}
	fields(fn.Recv)
	fields(fn.Type.Params)
	fields(fn.Type.Results)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if n.Tok == token.DEFINE {
				for _, l := range n.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.ValueSpec:
			for _, id := range n.Names {
				out[id.Name] = true
			}
		case *ast.RangeStmt:
			if n.Tok == token.DEFINE {
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if id, ok := e.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		case *ast.FuncLit:
			fields(n.Type.Params)
			fields(n.Type.Results)
		case *ast.TypeSwitchStmt:
			if a, ok := n.Assign.(*ast.AssignStmt); ok {
				for _, l := range a.Lhs {
					if id, ok := l.(*ast.Ident); ok {
						out[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return out
}

// TestVarWrites finds tests writing package-level vars, theirs or another package's, other than through
// testhook.Set, which puts them back when the test ends.
func TestVarWrites(files []File, module string) []Hit {
	vars := packageVars(files, true)
	var hits []Hit
	for _, f := range files {
		if !isTest(f) {
			continue
		}
		for _, w := range writes(f, vars, module, false) {
			hits = append(hits, Hit{w.pos, w.v.dir + "." + w.v.name})
		}
	}
	return hits
}

// named says t is a named type other than a basic one: a value whose methods may change it.
func named(t ast.Expr) bool {
	switch t := t.(type) {
	case *ast.Ident:
		return !basic[t.Name] && t.Name != "bool"
	case *ast.SelectorExpr:
		return true
	}
	return false
}

func typeName(t ast.Expr) string {
	if n := callName(t); n != "" {
		return n
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return "value"
}
