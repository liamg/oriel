package lang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/toml"
)

// place is the file and 0-based line a breakpoint command names: {file, line}, or the cursor's.
func (x *Debug) place(a registry.Args) (string, int, error) {
	if file := a.String("file"); file != "" {
		line := int(a.Int("line"))
		if line < 1 {
			return "", 0, fmt.Errorf("no line %d", line)
		}
		return file, line - 1, nil
	}
	p := window(x.h)
	if p.Path == "" {
		return "", 0, errors.New("no file at the cursor")
	}
	return p.Path, p.Cursor.Line, nil
}

func (x *Debug) find(file string, line int) int {
	return slices.IndexFunc(x.bps[file], func(b debugBreakpoint) bool { return b.Line == line })
}

func (x *Debug) toggle(file string, line int) {
	if x.find(file, line) >= 0 {
		x.remove(file, line)
		return
	}
	x.set(file, debugBreakpoint{Line: line})
}

func (x *Debug) set(file string, b debugBreakpoint) {
	if i := x.find(file, b.Line); i >= 0 {
		x.bps[file][i] = b
	} else {
		x.bps[file] = append(x.bps[file], b)
		slices.SortFunc(x.bps[file], func(a, b debugBreakpoint) int { return a.Line - b.Line })
	}
	x.save()
	x.sync(file)
	x.changed()
}

func (x *Debug) remove(file string, line int) {
	i := x.find(file, line)
	if i < 0 {
		return
	}
	x.bps[file] = slices.Delete(x.bps[file], i, i+1)
	if len(x.bps[file]) == 0 {
		delete(x.bps, file)
	}
	x.save()
	x.sync(file)
	x.changed()
}

// sync sends a file's breakpoints to the running session.
func (x *Debug) sync(file string) {
	c := x.client()
	if c == nil {
		return
	}
	var bps []dap.Breakpoint
	for _, b := range x.bps[file] {
		bps = append(bps, b.dap())
	}
	x.async(func(ctx context.Context) error {
		_, err := c.SetBreakpoints(ctx, file, bps)
		return err
	}, nil)
}

func (x *Debug) dapBreakpoints() map[string][]dap.Breakpoint {
	out := map[string][]dap.Breakpoint{}
	for file, bs := range x.bps {
		for _, b := range bs {
			out[file] = append(out[file], b.dap())
		}
	}
	return out
}

// state is what's kept across restarts: the breakpoints and watches.
type debugState struct {
	Breakpoints map[string][]debugBreakpoint `json:"breakpoints"`
	Watches     []string                     `json:"watches"`
}

func (x *Debug) load() {
	path := x.StateFile
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var st debugState
	if json.Unmarshal(b, &st) == nil {
		if st.Breakpoints != nil {
			x.bps = st.Breakpoints
		}
		x.watches = st.Watches
	}
}

// save writes the breakpoints and watches, off the loop.
func (x *Debug) save() {
	path := x.StateFile
	if path == "" {
		return
	}
	b, _ := json.Marshal(debugState{Breakpoints: x.bps, Watches: x.watches})
	x.writes.Write(path, b)
}

// dir is the directory debugged: the window's project.
func (x *Debug) dir() string {
	if x.WorkDir != "" {
		return x.WorkDir
	}
	return editorDir(x.h)
}

// configs are the launch configs in .oriel/debug.toml, as [[debug]] tables.
func (x *Debug) configs() ([]dap.LaunchConfig, error) {
	dir := x.dir()
	src, err := os.ReadFile(filepath.Join(dir, ".oriel", "debug.toml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, fmt.Errorf(".oriel/debug.toml: %v", err)
	}
	tables, _ := doc.Value()["debug"].([]any)
	var out []dap.LaunchConfig
	for i, t := range tables {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		c, err := dap.ConfigFromMap(m, dir)
		if err != nil {
			return nil, fmt.Errorf(".oriel/debug.toml, config %d: %v", i+1, err)
		}
		if c.Name == "" {
			c.Name = fmt.Sprintf("config %d", i+1)
		}
		out = append(out, c)
	}
	return out, nil
}

// inferred is the launch config for the current file, from the first adapter for its language that has one.
func (x *Debug) inferred() (dap.LaunchConfig, bool) {
	path := window(x.h).Path
	lang, ok := kit.LanguageFor(x.h, path, nil)
	if path == "" || !ok {
		return dap.LaunchConfig{}, false
	}
	for _, s := range x.adapters() {
		if s.Debugs(lang.Name) && s.Infer != nil {
			var c dap.LaunchConfig
			registry.Call(func() error { c, ok = s.Infer(path); return nil })
			if ok {
				return c, true
			}
		}
	}
	return dap.LaunchConfig{}, false
}

// adapterFor is the first installed adapter for a config's type.
func (x *Debug) adapterFor(cfg dap.LaunchConfig) (dap.AdapterSpec, error) {
	var names []string
	for _, s := range x.adapters() {
		if s.Debugs(cfg.Type) {
			if dap.Installed(s) {
				return s, nil
			}
			names = append(names, s.Name)
		}
	}
	if len(names) == 0 {
		return dap.AdapterSpec{}, fmt.Errorf("no debug adapter for %s", cfg.Type)
	}
	return dap.AdapterSpec{}, fmt.Errorf("no debug adapter for %s is installed: %s", cfg.Type, strings.Join(names, ", "))
}

// startNamed starts the config of that name, or with none, the first configured, or the current file's.
func (x *Debug) startNamed(name string) error {
	cfgs, err := x.configs()
	if err != nil {
		return err
	}
	var cfg dap.LaunchConfig
	switch i := slices.IndexFunc(cfgs, func(c dap.LaunchConfig) bool { return c.Name == name }); {
	case name != "" && i >= 0:
		cfg = cfgs[i]
	case name != "":
		return fmt.Errorf("no launch config %q in .oriel/debug.toml", name)
	case len(cfgs) > 0:
		cfg = cfgs[0]
	default:
		c, ok := x.inferred()
		if !ok {
			return errors.New("nothing to debug: open a file an adapter debugs, or add a config to .oriel/debug.toml")
		}
		cfg = c
	}
	return x.startConfig(cfg)
}
