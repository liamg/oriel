// Package framecheck finds file, process and network I/O reachable from code that runs in a frame: a
// function that takes a *ui.Ctx or a uireg.ViewCtx builds a frame, and nothing it calls may stat, read,
// run a process or dial. Calls go through internal/fsx instead where the call graph here can't tell they run off the
// loop: fsx panics in a frame under the oriel_framecheck build tag.
//
// The call graph is read from the syntax: calls to functions in the same package or another of the module's, and a
// method's calls to its receiver's methods. A func literal whose first parameter is a
// context.Context is work run off the loop (plugin.RunSlow's, a cache's fetch), as is a go statement's and one handed to a
// Go (plugin.Go, kit.Busy.Go).
package framecheck

import (
	"fmt"
	"go/ast"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/liamg/oriel/tools/ratchet"
)

// io are the calls that do I/O, by import path, or the whole package when its list is nil.
var io = map[string][]string{
	"os": {"Stat", "Lstat", "ReadFile", "ReadDir", "Open", "OpenFile", "Create", "WriteFile", "Mkdir", "MkdirAll",
		"MkdirTemp", "CreateTemp", "Remove", "RemoveAll", "Rename", "Chmod", "Chtimes", "Symlink", "Readlink", "Getwd",
		"Chdir", "StartProcess", "Truncate", "Link"},
	"os/exec":       nil,
	"net/http":      {"Get", "Post", "PostForm", "Head"},
	"net":           {"Dial", "DialTimeout", "Listen", "ListenPacket", "LookupHost", "LookupIP", "LookupAddr", "LookupCNAME"},
	"path/filepath": {"Walk", "WalkDir", "Glob", "EvalSymlinks", "Abs"},
	"io/ioutil":     nil,
}

// Hit is I/O reachable from a frame: the call, and the chain of functions from the frame's code to it.
type Hit struct {
	Call  string   // "os.ReadFile"
	Chain []string // "app/kit.(*FileSrc).Get", …, the function making the call last
	Pos   string   // where the call is
}

// Key names a hit without line numbers: the frame's function, the function making the call, and the call.
func (h Hit) Key() string {
	return h.Chain[0] + " → " + h.Chain[len(h.Chain)-1] + ": " + h.Call
}

func (h Hit) String() string {
	return fmt.Sprintf("%s: %s, reached in a frame by %s", h.Pos, h.Call, strings.Join(h.Chain, " → "))
}

type fn struct {
	name  string // "app/kit.(*FileSrc).Get"
	dir   string
	file  ratchet.File
	body  []ast.Node
	root  bool
	calls []string // keys of the functions it calls
	io    []ioCall
	lits  int    // its func literals that build frames, named in order
	recv  string // a method's receiver, as it's named in it and its func literals
	typ   string // and its type
}

type ioCall struct{ call, pos string }

// Check reads files, every non-test Go file of the module's packages, and answers the I/O reachable from a frame.
func Check(files []ratchet.File, module string) []Hit {
	g := &graph{fns: map[string]*fn{}, methods: map[string]string{}, module: module}
	for _, f := range files {
		g.declare(f)
	}
	decls := make([]*fn, 0, len(g.fns))
	for _, f := range g.fns {
		decls = append(decls, f)
	}
	for _, f := range decls {
		g.scan(f)
	}
	return g.reach()
}

type graph struct {
	fns     map[string]*fn
	methods map[string]string // dir + "." + type + "." + method → the method's key
	module  string
}

func (g *graph) declare(f ratchet.File) {
	dir := path.Dir(f.Path)
	for _, d := range f.AST.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		name := fd.Name.Name
		var recv, typ string
		if fd.Recv != nil && len(fd.Recv.List) > 0 {
			r := fd.Recv.List[0]
			name = recvName(r.Type) + "." + name
			typ = strings.Trim(recvName(r.Type), "(*)")
			if len(r.Names) > 0 {
				recv = r.Names[0].Name
			}
			g.methods[dir+"."+typ+"."+fd.Name.Name] = dir + "." + name
		}
		x := &fn{name: dir + "." + name, dir: dir, file: f, body: []ast.Node{fd.Body}, root: buildsFrame(fd.Type, dir), recv: recv, typ: typ}
		g.fns[x.name] = x
	}
}

func recvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return "(*" + recvName(t.X) + ")"
	case *ast.IndexExpr:
		return recvName(t.X)
	case *ast.IndexListExpr:
		return recvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return "?"
}

// buildsFrame says a function of type t, in dir, builds a frame: it takes a *ui.Ctx or a uireg.ViewCtx.
func buildsFrame(t *ast.FuncType, dir string) bool {
	for _, p := range t.Params.List {
		e := p.Type
		if s, ok := e.(*ast.StarExpr); ok {
			e = s.X
		}
		switch e := e.(type) {
		case *ast.SelectorExpr:
			if x, ok := e.X.(*ast.Ident); ok && (x.Name == "ui" && e.Sel.Name == "Ctx" || x.Name == "uireg" && e.Sel.Name == "ViewCtx") {
				return true
			}
		case *ast.Ident:
			if dir == "ui" && e.Name == "Ctx" {
				return true
			}
		}
	}
	return false
}

// spawns says a call runs the func literals it's given on a goroutine of their own, as a go statement does: a Go
// (plugin.Go, kit.Busy's, a sync.WaitGroup's).
func spawns(c *ast.CallExpr) bool {
	switch e := c.Fun.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name == "Go"
	case *ast.Ident:
		return e.Name == "Go"
	}
	return false
}

// offLoop says a func literal is work run off the loop: its first parameter is a context.Context.
func offLoop(t *ast.FuncType) bool {
	if len(t.Params.List) == 0 {
		return false
	}
	s, ok := t.Params.List[0].Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := s.X.(*ast.Ident)
	return ok && x.Name == "context" && s.Sel.Name == "Context"
}

// scan finds f's calls; a func literal that builds a frame becomes a function of its own, a root.
func (g *graph) scan(f *fn) {
	imports := map[string]string{}
	for _, im := range f.file.AST.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		name := path.Base(p)
		if im.Name != nil {
			name = im.Name.Name
		}
		imports[name] = p
	}
	for _, b := range f.body {
		ast.Inspect(b, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.GoStmt:
				return false
			case *ast.FuncLit:
				if offLoop(n.Type) {
					return false
				}
				if buildsFrame(n.Type, f.dir) {
					f.lits++
					lit := &fn{name: fmt.Sprintf("%s.func%d", f.name, f.lits), dir: f.dir, file: f.file, body: []ast.Node{n.Body}, root: true,
						recv: f.recv, typ: f.typ}
					g.fns[lit.name] = lit
					g.scan(lit)
					f.calls = append(f.calls, lit.name)
					return false
				}
			case *ast.CallExpr:
				g.call(f, imports, n)
				if spawns(n) {
					for _, a := range n.Args {
						if _, lit := a.(*ast.FuncLit); !lit {
							ast.Inspect(a, func(m ast.Node) bool {
								if c, ok := m.(*ast.CallExpr); ok {
									g.call(f, imports, c)
								}
								return true
							})
						}
					}
					return false
				}
			}
			return true
		})
	}
}

func (g *graph) call(f *fn, imports map[string]string, c *ast.CallExpr) {
	switch e := c.Fun.(type) {
	case *ast.Ident:
		f.calls = append(f.calls, f.dir+"."+e.Name)
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if p, isPkg := imports[identName(x, ok)]; ok && isPkg {
			if names, bad := io[p]; bad && (names == nil || contains(names, e.Sel.Name)) {
				f.io = append(f.io, ioCall{call: path.Base(p) + "." + e.Sel.Name, pos: f.file.Fset.Position(c.Pos()).String()})
			} else if rel, in := strings.CutPrefix(p, g.module+"/"); in {
				f.calls = append(f.calls, rel+"."+e.Sel.Name)
			}
			return
		}
		if ok && f.recv != "" && x.Name == f.recv {
			if m, ok := g.methods[f.dir+"."+f.typ+"."+e.Sel.Name]; ok {
				f.calls = append(f.calls, m)
			}
		}
	}
}

func identName(x *ast.Ident, ok bool) string {
	if !ok {
		return ""
	}
	return x.Name
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// reach walks from each root to the I/O it reaches, the first chain found to each call.
func (g *graph) reach() []Hit {
	var roots []string
	for k, f := range g.fns {
		if f.root {
			roots = append(roots, k)
		}
	}
	sort.Strings(roots)
	seen := map[string]bool{}
	var out []Hit
	var walk func(k string, chain []string)
	walk = func(k string, chain []string) {
		f := g.fns[k]
		if f == nil || seen[k] {
			return
		}
		seen[k] = true
		chain = append(chain, k)
		for _, c := range f.io {
			out = append(out, Hit{Call: c.call, Chain: append([]string(nil), chain...), Pos: c.pos})
		}
		for _, next := range f.calls {
			if g.fns[next] != nil && !g.fns[next].root {
				walk(next, chain)
			}
		}
	}
	for _, r := range roots {
		walk(r, nil)
	}
	return out
}
