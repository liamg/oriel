package app

import (
	"errors"
	"fmt"
	"maps"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/testrun"
)

// debugTest debugs one test, subtest or table row through its adapter's debug launch (debug.launch): {key} as
// discovered in {file}, stopping at {line} (1-based) of the file when it's given, on a breakpoint set there unless
// one is.
func (x *testsExt) debugTest(ctx registry.Ctx, a registry.Args) error {
	file := a.String("file")
	it, adapter, ok := x.item(file, a.String("key"))
	if !ok {
		return fmt.Errorf("no test %s", a.String("key"))
	}
	cmd, err := adapter.Command(x.workspace(), []testrun.Item{it}, testrun.ModeDebug)
	if errors.Is(err, testrun.ErrUnsupported) || err == nil && cmd.Launch == nil {
		return fmt.Errorf("%s tests can't be debugged yet", adapter.Name())
	}
	if err != nil {
		return err
	}
	if line := a.Int("line"); line > 0 {
		if err := x.h.Run(ctx, "debug.breakpoint.set", registry.Args{"file": file, "line": line, "keep": true}); err != nil {
			return err
		}
	}
	return x.h.Run(ctx, "debug.launch", launchArgs(cmd.Launch))
}

// item is the test with key discovered in file, or in any buffer discovered.
func (x *testsExt) item(file, key string) (testrun.Item, testrun.Adapter, bool) {
	if f, ok := x.sh.editor.openBuffer(file); ok {
		d := x.discover(file, f.Text)
		for _, it := range d.items {
			if it.Key() == key {
				return it, d.adapter, true
			}
		}
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, d := range x.found {
		for _, it := range d.items {
			if it.Key() == key {
				return it, d.adapter, true
			}
		}
	}
	return testrun.Item{}, nil, false
}

// launchArgs is a launch as debug.launch takes it: a .oriel/debug.toml table.
func launchArgs(l *testrun.Launch) registry.Args {
	out := registry.Args{}
	maps.Copy(out, l.Extra)
	out["name"], out["type"] = l.Name, l.Type
	if l.Program != "" {
		out["program"] = l.Program
	}
	if l.Cwd != "" {
		out["cwd"] = l.Cwd
	}
	if len(l.Args) > 0 {
		args := make([]any, len(l.Args))
		for i, s := range l.Args {
			args[i] = s
		}
		out["args"] = args
	}
	if len(l.Env) > 0 {
		env := map[string]any{}
		for k, v := range l.Env {
			env[k] = v
		}
		out["env"] = env
	}
	return out
}
