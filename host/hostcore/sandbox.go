package hostcore

import (
	"context"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
)

type SandboxFactory = hostapi.SandboxFactory

type SandboxAgent = hostapi.SandboxAgent

type Approve = hostapi.Approve

type Approval = hostapi.Approval

type Sandbox = hostapi.Sandbox

// Keyed runs fn, a git operation of session id's that may wait on a security-key touch, asking for the touch while
// it waits; what and project say what it is on the card.
type Keyed func(ctx context.Context, id core.SessionID, what, project string, fn func(ctx context.Context) error) error

type ToolsReq = hostapi.ToolsReq
