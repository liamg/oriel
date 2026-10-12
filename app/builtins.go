package app

import (
	"context"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

func builtins() []ext.Extension {
	return []ext.Extension{&emptyRoot{}}
}

// emptyRoot is the root view of an app with nothing else installed.
type emptyRoot struct{ d registry.Disposable }

func (e *emptyRoot) Manifest() ext.Manifest { return ext.Manifest{ID: "oriel.root"} }

func (e *emptyRoot) Activate(_ context.Context, h ext.Host) error {
	e.d = h.View(RootView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.Column() }))
	return nil
}

func (e *emptyRoot) Deactivate(context.Context) error {
	e.d.Dispose()
	return nil
}
