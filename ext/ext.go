package ext

import (
	"context"
	"log/slog"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// Extension is a plugin in any tier; built-ins are extensions too.
type Extension interface {
	Manifest() Manifest
	Activate(ctx context.Context, h Host) error
	Deactivate(ctx context.Context) error
}

type Manifest struct {
	ID string
}

// Host is what an extension sees of the app.
type Host interface {
	registry.Registry
	Session(id core.SessionID) (core.Session, bool)
	Sessions() []core.Session
	Log() *slog.Logger
}
