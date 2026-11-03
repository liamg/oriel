package lang

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/registry"
)

// live says a session is running with an adapter to talk to.
func (x *Debug) live(registry.Args) bool { return x.sess.live() && x.client() != nil }

func (x *Debug) commands() []registry.Command {
	return slices.Concat(x.sessionCommands(), x.breakpointCommands(), x.watchCommands())
}

// sessionCommands start, drive and stop a session.
func (x *Debug) sessionCommands() []registry.Command {
	paused := func(registry.Args) bool { return x.live(nil) && x.sess.state == debugPaused }
	running := func(registry.Args) bool { return x.live(nil) && x.sess.state == debugRunning }
	step := func(kind dap.StepKind) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error { return x.step(kind) }
	}
	return []registry.Command{
		{ID: "debug.start", Title: "Start debugging: the launch config {config} by name, else the first in .oriel/debug.toml, else one for the current file",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.startNamed(a.String("config")) }},
		{ID: "debug.launch", Title: "Start debugging a launch config given whole: {name, type, request, program, args, cwd, env, pid} and the adapter's own keys, as in .oriel/debug.toml",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.launch(a) }},
		{ID: "Debug", Title: "Start debugging; :Debug {config}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.startNamed(strings.TrimSpace(a.String("args")))
		}},
		{ID: "debug.continue", Title: "Continue, or start debugging when nothing is", Run: func(registry.Ctx, registry.Args) error {
			if !x.sess.live() {
				return x.startNamed("")
			}
			return x.resume()
		}},
		{ID: "Dc", Title: "Continue (:Dc)", Available: paused, Run: func(registry.Ctx, registry.Args) error { return x.resume() }},
		{ID: "debug.pause", Title: "Pause the debuggee", Available: running, Run: func(registry.Ctx, registry.Args) error { return x.pause() }},
		{ID: "debug.step.over", Title: "Step over", Available: paused, Run: step(dap.StepOver)},
		{ID: "debug.step.into", Title: "Step into", Available: paused, Run: step(dap.StepIn)},
		{ID: "debug.step.out", Title: "Step out", Available: paused, Run: step(dap.StepOut)},
		{ID: "debug.stop", Title: "Stop debugging", Available: x.live, Run: func(registry.Ctx, registry.Args) error { return x.stopSession(nil) }},
		{ID: "debug.restart", Title: "Restart the debug session", Available: x.live, Run: func(registry.Ctx, registry.Args) error {
			cfg, spec := x.sess.cfg, x.sess.spec
			return x.stopSession(func() { x.start(spec, cfg) })
		}},
		{ID: "debug.frame", Title: "Select a frame of the stack: {index}, 0 the innermost", Available: paused, Run: func(_ registry.Ctx, a registry.Args) error {
			return x.selectFrame(int(a.Int("index")))
		}},
		{ID: "debug.frame.up", Title: "Select the caller's frame", Available: paused, Run: func(registry.Ctx, registry.Args) error {
			return x.selectFrame(x.sess.frame + 1)
		}},
		{ID: "debug.frame.down", Title: "Select the callee's frame", Available: paused, Run: func(registry.Ctx, registry.Args) error {
			return x.selectFrame(x.sess.frame - 1)
		}},
	}
}

func (x *Debug) breakpointCommands() []registry.Command {
	return []registry.Command{
		{ID: "debug.breakpoint.toggle", Title: "Set or clear a breakpoint: {file} and 1-based {line}, the cursor's by default",
			Run: func(_ registry.Ctx, a registry.Args) error {
				file, line, err := x.place(a)
				if err != nil {
					return err
				}
				x.toggle(file, line)
				return nil
			}},
		{ID: "debug.breakpoint.set", Title: "Set a breakpoint at {file}:{line} (the cursor's by default) with a {condition}, {hit} condition or {log} message; {keep} leaves one already there as it is",
			Run: func(_ registry.Ctx, a registry.Args) error {
				file, line, err := x.place(a)
				if err != nil {
					return err
				}
				if a.Bool("keep") && x.find(file, line) >= 0 {
					return nil
				}
				x.set(file, debugBreakpoint{Line: line, Condition: a.String("condition"), HitCondition: a.String("hit"), LogMessage: a.String("log")})
				return nil
			}},
		{ID: "Breakpoint", Title: "Set a breakpoint on the cursor's line; :Breakpoint {condition} makes it conditional",
			Run: func(_ registry.Ctx, a registry.Args) error {
				file, line, err := x.place(nil)
				if err != nil {
					return err
				}
				x.set(file, debugBreakpoint{Line: line, Condition: strings.TrimSpace(a.String("args"))})
				return nil
			}},
		{ID: "Logpoint", Title: "Set a logpoint on the cursor's line: :Logpoint {message}, {expressions} in braces",
			Run: func(_ registry.Ctx, a registry.Args) error {
				msg := strings.TrimSpace(a.String("args"))
				if msg == "" {
					return errors.New("a logpoint needs a message: :Logpoint x is {x}")
				}
				file, line, err := x.place(nil)
				if err != nil {
					return err
				}
				x.set(file, debugBreakpoint{Line: line, LogMessage: msg})
				return nil
			}},
		{ID: "debug.breakpoint.remove", Title: "Remove the breakpoint at {file}:{line}", Run: func(_ registry.Ctx, a registry.Args) error {
			file, line, err := x.place(a)
			if err != nil {
				return err
			}
			x.remove(file, line)
			return nil
		}},
		{ID: "debug.breakpoints.clear", Title: "Remove every breakpoint", Run: func(registry.Ctx, registry.Args) error {
			for file := range x.bps {
				x.bps[file] = nil
				x.sync(file)
			}
			x.bps = map[string][]debugBreakpoint{}
			x.save()
			x.changed()
			return nil
		}},
	}
}

// watchCommands watch and evaluate expressions, and show the panel.
func (x *Debug) watchCommands() []registry.Command {
	return []registry.Command{
		{ID: "debug.watch.add", Title: "Watch an {expression}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.addWatch(a.String("expression"))
		}},
		{ID: "Watch", Title: "Watch an expression: :Watch {expression}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.addWatch(strings.TrimSpace(a.String("args")))
		}},
		{ID: "debug.watch.remove", Title: "Stop watching the {index}th expression (0-based)", Run: func(_ registry.Ctx, a registry.Args) error {
			i := int(a.Int("index"))
			if i < 0 || i >= len(x.watches) {
				return fmt.Errorf("no watch %d", i)
			}
			x.watches = slices.Delete(x.watches, i, i+1)
			x.panel.watchVals = nil
			x.save()
			x.refresh()
			return nil
		}},
		{ID: "debug.eval", Title: "Evaluate an {expression} in the selected frame, in the debug console", Available: x.live,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.eval(a.String("expression")) }},
		{ID: "Eval", Title: "Evaluate in the debug console: :Eval {expression}", Available: x.live, Run: func(_ registry.Ctx, a registry.Args) error {
			return x.eval(strings.TrimSpace(a.String("args")))
		}},
		{ID: "debug.open", Title: "Show the Debug panel", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.h.Run(ctx, "view.open", debugViewArgs())
		}},
	}
}

func (x *Debug) keymaps() []registry.Keymap {
	var ks []registry.Keymap
	for _, k := range []registry.Keymap{
		{Keys: "<Leader>d", Desc: "+debug"},
		{Keys: "<Leader>db", Command: "debug.breakpoint.toggle", Desc: "Toggle breakpoint"},
		{Keys: "<Leader>dc", Command: "debug.continue", Desc: "Continue or start"},
		{Keys: "<Leader>do", Command: "debug.open", Desc: "Debug panel"},
		{Keys: "<Leader>dx", Command: "debug.stop", Desc: "Stop"},
		{Keys: "]f", Command: "debug.frame.up", Desc: "Caller's frame"},
		{Keys: "[f", Command: "debug.frame.down", Desc: "Callee's frame"},
	} {
		k.Mode = "n"
		ks = append(ks, k)
	}
	for _, k := range []registry.Keymap{
		{Keys: "<F5>", Command: "debug.continue", Desc: "Continue, or start debugging"},
		{Keys: "<F6>", Command: "debug.pause", Desc: "Pause"},
		{Keys: "<F9>", Command: "debug.breakpoint.toggle", Desc: "Toggle breakpoint"},
		{Keys: "<F10>", Command: "debug.step.over", Desc: "Step over"},
		{Keys: "<F11>", Command: "debug.step.into", Desc: "Step into"},
		{Keys: "<S-F11>", Command: "debug.step.out", Desc: "Step out"},
		{Keys: "<S-F5>", Command: "debug.stop", Desc: "Stop debugging"},
		{Keys: "<D-S-F5>", Command: "debug.restart", Desc: "Restart debugging"},
	} {
		k.Mode = "global"
		ks = append(ks, k)
	}
	return ks
}
