package hostcore

import (
	"context"

	"github.com/liamg/oriel/core"
)

// Keyed runs fn, a git operation of session id's that may wait on a security-key touch, asking for the touch while
// it waits; what and project say what it is on the card.
type Keyed func(ctx context.Context, id core.SessionID, what, project string, fn func(ctx context.Context) error) error
