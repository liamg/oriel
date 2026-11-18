package lang

import (
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/testrun"
)

// outputCommands show tests' output in the Tests panel.
func (x *Tests) outputCommands() []registry.Command {
	return []registry.Command{
		{ID: "test.output.open", Grant: registry.Open, Title: "Show the output of the test at the cursor", Args: registry.NoArgs, Run: func(ctx registry.Ctx, _ registry.Args) error {
			it, ok := x.nearest()
			if !ok {
				return fmt.Errorf("no test at the cursor")
			}
			return x.showOutput(ctx, it.Key(), outputCase)
		}},
		{ID: "tests.output.open", Grant: registry.Open, Title: "Show the last run's output in the Tests panel", Args: registry.NoArgs, Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.showOutput(ctx, x.panel.sel, outputRun)
		}},
		{ID: "test.attach", Grant: registry.Open, Title: "Follow the running test's output", Args: registry.NoArgs, Run: func(ctx registry.Ctx, _ registry.Args) error {
			key, ok := x.running()
			if !ok {
				return fmt.Errorf("no test running")
			}
			return x.showOutput(ctx, key, outputCase)
		}},
	}
}

// showOutput opens the Tests panel on key's output: an output*.
func (x *Tests) showOutput(ctx registry.Ctx, key string, which int) error {
	x.panel.sel, x.panel.reveal, x.panel.out = key, key, which
	return x.h.Run(ctx, "tests.open", nil)
}

// running is the key of the test the run in progress is running, the latest to start, or else its first in scope.
func (x *Tests) running() (string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.live == nil {
		return "", false
	}
	for i := len(x.live.Tests) - 1; i >= 0; i-- {
		if r := x.live.Tests[i]; r.Status == testrun.StatusRunning {
			return r.Key(), true
		}
	}
	if len(x.live.Scope) > 0 {
		return x.live.Scope[0], true
	}
	return "", false
}
