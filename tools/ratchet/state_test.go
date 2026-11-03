package ratchet

import (
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

func parsed(t *testing.T, path, src string) File {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	return File{Path: path, Fset: fset, AST: f}
}

func whats(hits []Hit) []string {
	var out []string
	for _, h := range hits {
		out = append(out, h.What)
	}
	slices.Sort(out)
	return out
}

// State is a var written after it's declared, or one holding a lock, an atomic or something made; tables, init's
// writes and allowed vars aren't.
func TestMutableVars(t *testing.T) {
	files := []File{
		parsed(t, "p/a.go", `package p

import ("regexp"; "sync")

var table = map[string]int{"a": 1}
var re = regexp.MustCompile("x")
var cache = map[string]int{}
var mu sync.Mutex
var made = newThing()
var fromInit []string
var allowed = newThing()

func init() { fromInit = append(fromInit, "x") }

func put(k string) { cache[k] = 1 }

func local() { cache := map[string]int{}; cache["x"] = 1 }
`),
		parsed(t, "p/a_test.go", `package p

func TestX() { table["b"] = 2 }
`),
	}
	got := whats(MutableVars(files, "m", map[string]string{"p.allowed": "why"}))
	want := []string{"cache written after it's declared", "made holds a lock, atomic, channel or pointer", "mu is a sync.Mutex for its methods to change"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A test's write to a package-level var counts, its own package's or another's, but not through testhook.Set.
func TestTestVarWrites(t *testing.T) {
	files := []File{
		parsed(t, "p/a.go", "package p\n\nvar Knob = 1\nvar knob = 2\n"),
		parsed(t, "p/a_test.go", `package p

func TestA(t *testing.T) { knob = 3; testhook.Set(t, &knob, 4); x := 1; x++ }
`),
		parsed(t, "q/q_test.go", `package q

import "m/p"

func TestB(t *testing.T) { p.Knob = 5 }
`),
	}
	if got := whats(TestVarWrites(files, "m")); !slices.Equal(got, []string{"p.Knob", "p.knob"}) {
		t.Errorf("got %q", got)
	}
}
