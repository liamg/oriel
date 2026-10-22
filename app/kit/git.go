package kit

import (
	"context"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
)

// GitOf is the window's git trees (registry.GitTreesService), or ones that know no repo.
func GitOf(r registry.Registry) registry.GitTrees {
	if t, ok := registry.GitTreesOf(r); ok {
		return t
	}
	return noTrees{}
}

type noTrees struct{}

func (noTrees) Tree(string) registry.GitTree       { return noTree{} }
func (noTrees) Holding(string) registry.GitTree    { return noTree{} }
func (noTrees) Do(func(ctx context.Context) error) {}
func (noTrees) Reload()                            {}
func (noTrees) Busy() bool                         { return false }

// noTree is outside any repo.
type noTree struct{}

func (noTree) Root() string         { return "" }
func (noTree) State() *gitx.State   { return nil }
func (noTree) Remote() string       { return "" }
func (noTree) Status() *gitx.Status { return nil }
func (noTree) Rel(p string) string  { return p }
func (noTree) Load() registry.Load  { return registry.Load{Done: true} }
