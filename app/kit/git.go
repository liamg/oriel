package kit

import (
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
)

// GitOf is the window's git trees (registry.GitTreesService), or ones that know no repo.
func GitOf(r registry.Registry) registry.GitTrees {
	if t, ok := registry.GitTreesOf(r); ok {
		return t
	}
	return noTrees{r}
}

// noTrees is a window with no git service: what it's asked to write says so rather than vanish.
type noTrees struct{ r registry.Registry }

func (noTrees) Tree(string) registry.GitTree              { return noTree{} }
func (noTrees) Holding(string) registry.GitTree           { return noTree{} }
func (noTrees) WatchWindow(func(string, []string)) func() { return func() {} }
func (t noTrees) Run(func(ctx context.Context) error) {
	if n, ok := t.r.(interface{ Notify(core.Notice) }); ok {
		n.Notify(core.Notice{ID: "git.none", Severity: core.SevError, Source: "git", Text: "There's no git service in this window to run that."})
	}
}
func (noTrees) Reload()           {}
func (noTrees) Scope() gitx.Scope { return gitx.All }
func (noTrees) Busy() bool        { return false }

// noTree is outside any repo.
type noTree struct{}

func (noTree) Root() string         { return "" }
func (noTree) State() *gitx.State   { return nil }
func (noTree) Counted() *gitx.State { return nil }
func (noTree) Remote() string       { return "" }
func (noTree) Status() *gitx.Status { return nil }
func (noTree) Rel(p string) string  { return p }
func (noTree) Load() registry.Load  { return registry.Load{Done: true} }
