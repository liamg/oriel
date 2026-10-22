package lang

import (
	"fmt"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/testrun"
)

// outputCommands show tests' output in the Tests panel.
func (x *Tests) outputCommands() []registry.Command {
	return []registry.Command{
		{ID: "test.output", Title: "Show the output of the test at the cursor", Run: func(ctx registry.Ctx, _ registry.Args) error {
			it, ok := x.nearest()
			if !ok {
				return fmt.Errorf("no test at the cursor")
			}
			return x.showOutput(ctx, it.Key(), 0)
		}},
		{ID: "tests.output", Title: "Show the last run's output in the Tests panel", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.showOutput(ctx, x.panel.sel, 1)
		}},
		{ID: "test.attach", Title: "Follow the running test's output", Run: func(ctx registry.Ctx, _ registry.Args) error {
			key, ok := x.running()
			if !ok {
				return fmt.Errorf("no test running")
			}
			return x.showOutput(ctx, key, 0)
		}},
	}
}

// showOutput opens the Tests panel on key's output: which 0 its own, 1 the whole run's.
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
