package rig

import (
	"context"
	"os"

	"github.com/liamg/oriel/sandbox"
)

// EchoBackend runs an echo in place of the agent's sandbox: everything else of the sandbox (its socket, gateway and
// egress session) is real.
type EchoBackend struct{}

func (EchoBackend) Name() string { return "test" }
func (EchoBackend) Launch(context.Context, string, string, []string, sandbox.LaunchOptions) (sandbox.Command, error) {
	return sandbox.Command{Argv: []string{"echo", "in the sandbox"}, Env: os.Environ()}, nil
}
func (EchoBackend) Available() error                                         { return nil }
func (EchoBackend) Stop(context.Context, string) error                       { return nil }
func (EchoBackend) Health(context.Context, sandbox.Config) []sandbox.Problem { return nil }
func (EchoBackend) Sweep(context.Context, func(string) bool) []string        { return nil }
func (EchoBackend) AgentRunDir(string) string                                { return "/run/oriel" }
func (EchoBackend) ServesPort() bool                                         { return false }
