package testrun

import (
	"sync"

	"github.com/liamg/oriel/syntax"
)

// ContributionKind is the registry contribution kind a plugin adds an adapter with.
const ContributionKind = "testrun.adapter"

// Contribution is an adapter as a registry contribution: register it with registry.Contribute, and the app adds
// every one of this kind to its Set (Contribute).
type Contribution struct {
	Adapter Adapter
}

func (Contribution) ContributionKind() string   { return ContributionKind }
func (c Contribution) ContributionName() string { return c.Adapter.Name() }

// Set is the adapters a workspace's tests can be run with. An adapter registered later shadows an earlier one of
// the same name until it's removed. It's safe for concurrent use.
type Set struct {
	// Languages are the languages a file's tests are parsed in: the window's, so a language a plugin adds has its
	// tests.scm used too; the built-ins if nil.
	Languages func() *syntax.Set

	mu   sync.Mutex
	list []*entry
}

func (s *Set) langs() *syntax.Set {
	if s.Languages == nil {
		return syntax.Builtin()
	}
	return s.Languages()
}

type entry struct{ a Adapter }

// NewSet is a set of adapters, in order.
func NewSet(as ...Adapter) *Set {
	s := &Set{}
	for _, a := range as {
		s.Register(a)
	}
	return s
}

// Builtin is a set of Oriel's own adapters: Go, pytest, vitest, jest and cargo.
func Builtin() *Set {
	return NewSet(&Go{}, &Pytest{}, &Vitest{}, &Jest{}, &Cargo{})
}

// Register adds an adapter, and returns what removes it.
func (s *Set) Register(a Adapter) (remove func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := &entry{a}
	s.list = append(s.list, e)
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, x := range s.list {
			if x == e {
				s.list = append(s.list[:i:i], s.list[i+1:]...)
				return
			}
		}
	}
}

// Contribute registers the adapters among registry contributions (registry.Contributions(ContributionKind)); the
// rest are left alone.
func Contribute[C any](s *Set, cs []C) (remove func()) {
	var removes []func()
	for _, c := range cs {
		if tc, ok := any(c).(Contribution); ok && tc.Adapter != nil {
			removes = append(removes, s.Register(tc.Adapter))
		}
	}
	return func() {
		for _, r := range removes {
			r()
		}
	}
}

// Adapters is every adapter, newest first, one per name.
func (s *Set) Adapters() []Adapter {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Adapter
	seen := map[string]bool{}
	for i := len(s.list) - 1; i >= 0; i-- {
		a := s.list[i].a
		if !seen[a.Name()] {
			seen[a.Name()] = true
			out = append(out, a)
		}
	}
	return out
}

// Adapter finds one by name.
func (s *Set) Adapter(name string) (Adapter, bool) {
	for _, a := range s.Adapters() {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// Detect is the adapters that recognise the workspace, newest first.
func (s *Set) Detect(ws Workspace) []Adapter {
	var out []Adapter
	for _, a := range s.Adapters() {
		if a.Detect(ws) {
			out = append(out, a)
		}
	}
	return out
}

// Discover finds the tests in a buffer with the first of the workspace's adapters that finds any.
func (s *Set) Discover(ws Workspace, src Source) (Adapter, []Item) {
	if src.Languages == nil {
		src.Languages = s.langs()
	}
	for _, a := range s.Detect(ws) {
		if items := a.Discover(src); len(items) > 0 {
			return a, items
		}
	}
	return nil, nil
}

// Affected is, per adapter of the workspace that can tell, the items to run again after file was saved.
func (s *Set) Affected(ws Workspace, file string) (map[Adapter][]Item, error) {
	out := map[Adapter][]Item{}
	for _, a := range s.Detect(ws) {
		w, ok := a.(Watcher)
		if !ok {
			continue
		}
		items, err := w.Affected(ws, file)
		if err != nil {
			return out, err
		}
		if len(items) > 0 {
			out[a] = items
		}
	}
	return out, nil
}

// TestFile says whether any adapter that can tell (TestFiler) takes path for a test file.
func (s *Set) TestFile(path string) bool {
	for _, a := range s.Adapters() {
		if f, ok := a.(TestFiler); ok && f.TestFile(path) {
			return true
		}
	}
	return false
}
