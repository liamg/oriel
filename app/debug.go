package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/toml"
	"github.com/liamg/oriel/ui"
)

// DebugView is the Debug panel: call stack, breakpoints, variables, watch and the debug console (ui.md 08).
const DebugView = "debug"

var (
	tBreakpoint      = theme.Declare("debug.breakpoint", "error")
	tBreakpointFaint = theme.Declare("debug.breakpoint.unverified", "fg.faint")
	tDebugFrame      = theme.Declare("debug.frame", "accent")
	tDebugFrameBg    = theme.DeclareOn("debug.frame.bg", "alpha(accent, 10%)", "bg.base")
	tDebugPaused     = theme.Declare("debug.paused", "status.working")
	tDebugNote       = theme.Declare("debug.note", "fg.faint")
)

// Session states.
const (
	debugStarting = "starting"
	debugRunning  = "running"
	debugPaused   = "paused"
	debugEnded    = "ended"
)

// debugBreakpoint is a breakpoint the user set, kept per work directory whether or not a session runs.
type debugBreakpoint struct {
	Line         int    `json:"line"` // 0-based
	Condition    string `json:"condition,omitempty"`
	HitCondition string `json:"hit,omitempty"`
	LogMessage   string `json:"log,omitempty"`
}

func (b debugBreakpoint) dap() dap.Breakpoint {
	return dap.Breakpoint{Line: b.Line, Condition: b.Condition, HitCondition: b.HitCondition, LogMessage: b.LogMessage}
}

// glyph is its gutter mark: a dot, a ring with a condition, a diamond for a logpoint.
func (b debugBreakpoint) glyph() string {
	switch {
	case b.LogMessage != "":
		return "◆"
	case b.Condition != "" || b.HitCondition != "":
		return "○"
	}
	return "●"
}

// note is what it has besides its line: "cond v < 0 · hit == 2 · log x={x}".
func (b debugBreakpoint) note() string {
	var parts []string
	if b.Condition != "" {
		parts = append(parts, "cond "+b.Condition)
	}
	if b.HitCondition != "" {
		parts = append(parts, "hit "+b.HitCondition)
	}
	if b.LogMessage != "" {
		parts = append(parts, "log "+b.LogMessage)
	}
	return strings.Join(parts, " · ")
}

// consoleLine is a line of the debug console.
type consoleLine struct {
	Kind string // input, agent (the agent's input), result, error, stdout, stderr, console
	Text string
}

// watchValue is a watch expression's value in the selected frame.
type watchValue struct {
	Value string
	Err   bool
}

// debugSession is the session running, or the last one.
type debugSession struct {
	client *dap.Client
	spec   dap.AdapterSpec
	cfg    dap.LaunchConfig
	state  string
	stop   dap.Stopped
	thread int
	frames []dap.Frame
	frame  int                    // the selected frame's index
	scopes []dap.Scope            // the selected frame's
	vars   map[int][]dap.Variable // fetched children, by reference
	hits   map[int]int            // breakpoint id → times hit
	exit   *int
	gen    int // moves on each stop and resume, so an answer about an old stop is dropped
}

func (s *debugSession) live() bool { return s != nil && s.state != debugEnded }

func (s *debugSession) top() (dap.Frame, bool) {
	if s == nil || s.state != debugPaused || s.frame >= len(s.frames) {
		return dap.Frame{}, false
	}
	return s.frames[s.frame], true
}

// DebugStopped is the DebugStopped hook's event: the debuggee stopped, and where.
type DebugStopped struct {
	Adapter string `json:"adapter"`
	Reason  string `json:"reason"`
	Thread  int    `json:"thread"`
	File    string `json:"file"`
	Line    int    `json:"line"` // 1-based
}

// DebugEnded is the DebugEnded hook's event: a debug session ended.
type DebugEnded struct {
	Adapter  string `json:"adapter"`
	ExitCode int    `json:"exit_code"`
}

// debugExt is the debugger (DB-1, ide.md "Debugging"): breakpoints in the gutter (conditional, hit counts,
// logpoints), sessions on registered debug adapters (registry.DebugAdapter), stepping, the stack, variables, watch
// expressions and a REPL, in the Debug panel.
type debugExt struct {
	disposer
	sh *shell
	h  ext.Host

	bps     map[string][]debugBreakpoint // by absolute path
	watches []string
	sess    *debugSession
	console []consoleLine
	busy    sync.WaitGroup
	mu      sync.Mutex // guards sess.client for the goroutines that use it

	panel debugPanel
}

func (x *debugExt) Manifest() ext.Manifest { return ext.Manifest{ID: "debug"} }

func (x *debugExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.bps = map[string][]debugBreakpoint{}
	x.load()
	for _, s := range dap.Builtins() {
		x.add(h.Contribute(registry.DebugAdapter(s)))
	}
	x.add(h.Decorations("debug", registry.DecorationFunc(x.decorations)))
	x.add(h.Contribute(registry.GutterAction{Name: "debug.breakpoint", Command: "debug.breakpoint.toggle", Order: 50, When: x.debuggable,
		Source: "builtin:debug"}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "DebugStopped", Fired: true, Payload: DebugStopped{},
		Doc: "The debuggee stopped: at a breakpoint, after a step, on an exception or a pause."}))
	x.add(h.DeclareEvent(registry.EventInfo{Name: "DebugEnded", Fired: true, Payload: DebugEnded{}, Doc: "A debug session ended."}))
	x.commands(h)
	x.agentTools()
	x.add(h.View(DebugView, debugView{x}))
	x.add(h.View("statusline.left.66-debug", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "debug", Fn: x.statusSegment} })))
	return nil
}

func (x *debugExt) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	var c *dap.Client
	if x.sess != nil {
		c = x.sess.client
	}
	x.mu.Unlock()
	if c != nil {
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		c.Stop(sctx)
		cancel()
	}
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

func (x *debugExt) wait() { x.busy.Wait() }

func (x *debugExt) commands(h ext.Host) {
	live := func(registry.Args) bool { return x.sess.live() && x.client() != nil }
	paused := func(registry.Args) bool { return live(nil) && x.sess.state == debugPaused }
	running := func(registry.Args) bool { return live(nil) && x.sess.state == debugRunning }
	step := func(kind dap.StepKind) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error { return x.step(kind) }
	}
	for _, c := range []registry.Command{
		{ID: "debug.start", Title: "Start debugging: the launch config {config} by name, else the first in .oriel/debug.toml, else one for the current file",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.startNamed(a.String("config")) }},
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
		{ID: "debug.stop", Title: "Stop debugging", Available: live, Run: func(registry.Ctx, registry.Args) error { return x.stopSession(nil) }},
		{ID: "debug.restart", Title: "Restart the debug session", Available: live, Run: func(registry.Ctx, registry.Args) error {
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
		{ID: "debug.breakpoint.toggle", Title: "Set or clear a breakpoint: {file} and 1-based {line}, the cursor's by default",
			Run: func(_ registry.Ctx, a registry.Args) error {
				file, line, err := x.place(a)
				if err != nil {
					return err
				}
				x.toggle(file, line)
				return nil
			}},
		{ID: "debug.breakpoint.set", Title: "Set a breakpoint at {file}:{line} (the cursor's by default) with a {condition}, {hit} condition or {log} message",
			Run: func(_ registry.Ctx, a registry.Args) error {
				file, line, err := x.place(a)
				if err != nil {
					return err
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
			return nil
		}},
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
		{ID: "debug.eval", Title: "Evaluate an {expression} in the selected frame, in the debug console", Available: live,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.eval(a.String("expression")) }},
		{ID: "Eval", Title: "Evaluate in the debug console: :Eval {expression}", Available: live, Run: func(_ registry.Ctx, a registry.Args) error {
			return x.eval(strings.TrimSpace(a.String("args")))
		}},
		{ID: "debug.open", Title: "Show the Debug panel", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.open", debugViewArgs())
		}},
	} {
		x.add(h.Command(c))
	}
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
		x.add(h.Keymap(k))
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
		x.add(h.Keymap(k))
	}
}

func debugViewArgs() registry.Args {
	return registry.Args{"id": DebugView, "place": "dock", "edge": "bottom", "size": 300, "title": "Debug"}
}

// client is the running session's client, nil while it starts or once it's gone.
func (x *debugExt) client() *dap.Client {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.sess == nil {
		return nil
	}
	return x.sess.client
}

// adapters are the registered adapters as specs, in the order they're tried.
func (x *debugExt) adapters() []dap.AdapterSpec {
	var out []dap.AdapterSpec
	for _, a := range registry.DebugAdapters(x.h) {
		out = append(out, dap.AdapterSpec(a))
	}
	return out
}

// debuggable reports whether an adapter debugs the language of the file at path.
func (x *debugExt) debuggable(path string) bool {
	lang, ok := languageFor(x.h, path, nil)
	if !ok {
		return false
	}
	return slices.ContainsFunc(x.adapters(), func(s dap.AdapterSpec) bool { return s.Debugs(lang.Name) })
}

// place is the file and 0-based line a breakpoint command names: {file, line}, or the cursor's.
func (x *debugExt) place(a registry.Args) (string, int, error) {
	if file := a.String("file"); file != "" {
		line := int(a.Int("line"))
		if line < 1 {
			return "", 0, fmt.Errorf("no line %d", line)
		}
		return file, line - 1, nil
	}
	p := x.sh.editor.current()
	path := x.sh.editor.panePath(p)
	if path == "" {
		return "", 0, errors.New("no file at the cursor")
	}
	return path, p.Cursor().Line, nil
}

func (x *debugExt) find(file string, line int) int {
	return slices.IndexFunc(x.bps[file], func(b debugBreakpoint) bool { return b.Line == line })
}

func (x *debugExt) toggle(file string, line int) {
	if x.find(file, line) >= 0 {
		x.remove(file, line)
		return
	}
	x.set(file, debugBreakpoint{Line: line})
}

func (x *debugExt) set(file string, b debugBreakpoint) {
	if i := x.find(file, b.Line); i >= 0 {
		x.bps[file][i] = b
	} else {
		x.bps[file] = append(x.bps[file], b)
		slices.SortFunc(x.bps[file], func(a, b debugBreakpoint) int { return a.Line - b.Line })
	}
	x.save()
	x.sync(file)
}

func (x *debugExt) remove(file string, line int) {
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
}

// sync sends a file's breakpoints to the running session.
func (x *debugExt) sync(file string) {
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

func (x *debugExt) dapBreakpoints() map[string][]dap.Breakpoint {
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

func (x *debugExt) load() {
	path := x.sh.dirState("debug")
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

func (x *debugExt) save() {
	path := x.sh.dirState("debug")
	if path == "" {
		return
	}
	b, _ := json.Marshal(debugState{Breakpoints: x.bps, Watches: x.watches})
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, b, 0o644)
}

// dir is the directory debugged: the window's project.
func (x *debugExt) dir() string {
	if dir := x.sh.cfg.WorkDir; dir != "" {
		return dir
	}
	return x.sh.editor.dir
}

// configs are the launch configs in .oriel/debug.toml, as [[debug]] tables.
func (x *debugExt) configs() ([]dap.LaunchConfig, error) {
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
func (x *debugExt) inferred() (dap.LaunchConfig, bool) {
	path := x.sh.editor.panePath(x.sh.editor.current())
	lang, ok := languageFor(x.h, path, nil)
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
func (x *debugExt) adapterFor(cfg dap.LaunchConfig) (dap.AdapterSpec, error) {
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
func (x *debugExt) startNamed(name string) error {
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
	spec, err := x.adapterFor(cfg)
	if err != nil {
		return err
	}
	if x.sess.live() {
		return x.stopSession(func() { x.start(spec, cfg) })
	}
	x.start(spec, cfg)
	return nil
}

// start runs a session; its events come back on the loop.
func (x *debugExt) start(spec dap.AdapterSpec, cfg dap.LaunchConfig) {
	s := &debugSession{spec: spec, cfg: cfg, state: debugStarting, vars: map[int][]dap.Variable{}, hits: map[int]int{}}
	x.mu.Lock()
	x.sess = s
	x.mu.Unlock()
	x.console = []consoleLine{{Kind: "console", Text: fmt.Sprintf("%s · %s", spec.Name, cmpOr(cfg.Name, cfg.Program))}}
	x.panel.watchVals = nil
	x.h.Run(context.Background(), "view.open", debugViewArgs())
	opts := dap.Options{
		Stderr:      &logs.Lines{Log: x.sh.cfg.Logs.Logger("dap:" + spec.Name)},
		Breakpoints: x.dapBreakpoints(),
		OnEvent:     func(e dap.Event) { x.h.Do(func() { x.event(s, e) }) },
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		c, err := dap.Start(ctx, spec, cfg, opts)
		cancel()
		x.h.Do(func() {
			if err != nil {
				s.state = debugEnded
				x.print("error", err.Error())
				x.h.Notify(core.Notice{ID: "debug.start", Severity: core.SevError, Source: "debug", Title: "Couldn't start debugging", Text: err.Error()})
				return
			}
			x.mu.Lock()
			s.client = c
			x.mu.Unlock()
			if s.state == debugStarting {
				s.state = debugRunning
			}
			if s.state == debugPaused {
				x.fetch(s)
			}
			x.h.Redraw()
		})
		if err != nil {
			return
		}
		<-c.Done()
		x.h.Do(func() { x.ended(s) })
	}()
}

func (x *debugExt) ended(s *debugSession) {
	if s.state == debugEnded {
		return
	}
	s.state = debugEnded
	s.gen++
	code := 0
	if s.exit != nil {
		code = *s.exit
	}
	x.h.Fire(context.Background(), "DebugEnded", s.spec.Name, DebugEnded{Adapter: s.spec.Name, ExitCode: code})
	x.h.Redraw()
}

func (x *debugExt) print(kind, text string) {
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		x.console = append(x.console, consoleLine{Kind: kind, Text: l})
	}
	if over := len(x.console) - 2000; over > 0 {
		x.console = x.console[over:]
	}
}

// event applies what the adapter said to the session it's about.
func (x *debugExt) event(s *debugSession, e dap.Event) {
	defer x.h.Redraw()
	switch e := e.(type) {
	case dap.Stopped:
		s.state = debugPaused
		s.stop = e
		if e.Thread != 0 {
			s.thread = e.Thread
		}
		for _, id := range e.Breakpoints {
			s.hits[id]++
		}
		s.frame, s.frames, s.scopes = 0, nil, nil
		s.vars = map[int][]dap.Variable{}
		s.gen++
		if s.client != nil {
			x.fetch(s)
		}
	case dap.Continued:
		if s.state != debugEnded {
			s.state = debugRunning
			s.gen++
		}
	case dap.Output:
		if e.Category != "telemetry" {
			x.print(e.Category, e.Text)
		}
	case dap.Exited:
		code := e.Code
		s.exit = &code
		x.print("console", fmt.Sprintf("exited with code %d", e.Code))
	case dap.Terminated:
		x.ended(s)
	}
}

// fetch reads a stop's stack, then the selected frame.
func (x *debugExt) fetch(s *debugSession) {
	gen, c, thread := s.gen, s.client, s.thread
	x.async(func(ctx context.Context) error {
		frames, err := c.Stack(ctx, thread)
		if err != nil {
			return err
		}
		x.h.Do(func() {
			if s.gen != gen {
				return
			}
			s.frames = frames
			x.frameChanged(s)
			if f, ok := s.top(); ok {
				x.h.Fire(context.Background(), "DebugStopped", s.spec.Name, DebugStopped{Adapter: s.spec.Name, Reason: s.stop.Reason,
					Thread: s.thread, File: f.Path, Line: f.Line + 1})
			}
		})
		return nil
	}, nil)
}

// frameChanged shows the selected frame: its scopes and their variables, the watches, and its line in the editor.
func (x *debugExt) frameChanged(s *debugSession) {
	f, ok := s.top()
	if !ok {
		return
	}
	if f.Path != "" {
		x.h.Run(context.Background(), "editor.open", registry.Args{"path": f.Path, "line": f.Line + 1})
	}
	gen, frame, c := s.gen, s.frame, s.client
	x.async(func(ctx context.Context) error {
		scopes, err := c.Scopes(ctx, f.ID)
		if err != nil {
			return err
		}
		vars := map[int][]dap.Variable{}
		for _, sc := range scopes {
			if sc.Expensive {
				continue
			}
			if vs, err := c.Variables(ctx, sc.Ref); err == nil {
				vars[sc.Ref] = vs
			}
		}
		x.h.Do(func() {
			if s.gen != gen || s.frame != frame {
				return
			}
			s.scopes = scopes
			for ref, vs := range vars {
				s.vars[ref] = vs
			}
		})
		return nil
	}, nil)
	x.refresh()
}

// refresh evaluates the watches in the selected frame.
func (x *debugExt) refresh() {
	s := x.sess
	f, ok := s.top()
	if !ok || s.client == nil {
		return
	}
	gen, frame, c, exprs := s.gen, s.frame, s.client, slices.Clone(x.watches)
	x.async(func(ctx context.Context) error {
		vals := make([]watchValue, len(exprs))
		for i, e := range exprs {
			v, err := c.Evaluate(ctx, f.ID, e, dap.ContextWatch)
			if err != nil {
				vals[i] = watchValue{Value: debugErrText(err), Err: true}
			} else {
				vals[i] = watchValue{Value: v.Value}
			}
		}
		x.h.Do(func() {
			if s.gen == gen && s.frame == frame && slices.Equal(exprs, x.watches) {
				x.panel.watchVals = vals
			}
		})
		return nil
	}, nil)
}

func debugErrText(err error) string {
	var de *dap.Error
	if errors.As(err, &de) {
		return de.Message
	}
	return err.Error()
}

// expand fetches a structured variable's children.
func (x *debugExt) expand(ref int) {
	s := x.sess
	if s == nil || s.client == nil || ref <= 0 || s.vars[ref] != nil {
		return
	}
	s.vars[ref] = []dap.Variable{}
	gen, c := s.gen, s.client
	x.async(func(ctx context.Context) error {
		vs, err := c.Variables(ctx, ref)
		if err != nil {
			return err
		}
		x.h.Do(func() {
			if s.gen == gen {
				s.vars[ref] = vs
			}
		})
		return nil
	}, nil)
}

func (x *debugExt) selectFrame(i int) error {
	s := x.sess
	if s == nil || s.state != debugPaused {
		return errors.New("not paused")
	}
	if i < 0 || i >= len(s.frames) {
		return fmt.Errorf("no frame %d", i)
	}
	s.frame = i
	s.scopes = nil
	x.panel.watchVals = nil
	x.frameChanged(s)
	return nil
}

func (x *debugExt) addWatch(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return errors.New("nothing to watch")
	}
	x.watches = append(x.watches, expr)
	x.panel.watchVals = nil
	x.save()
	x.refresh()
	return nil
}

// eval evaluates in the REPL: in the selected frame while paused, globally otherwise.
func (x *debugExt) eval(expr string) error {
	expr = strings.TrimSpace(expr)
	c := x.client()
	if expr == "" || c == nil {
		return errors.New("nothing to evaluate")
	}
	x.print("input", expr)
	frame := 0
	if f, ok := x.sess.top(); ok {
		frame = f.ID
	}
	x.async(func(ctx context.Context) error {
		v, err := c.Evaluate(ctx, frame, expr, dap.ContextREPL)
		x.h.Do(func() {
			if err != nil {
				x.print("error", debugErrText(err))
				return
			}
			x.print("result", v.Value)
		})
		return nil
	}, nil)
	return nil
}

func (x *debugExt) resume() error {
	s, c := x.sess, x.client()
	if c == nil || s.state != debugPaused {
		return errors.New("not paused")
	}
	s.state, s.gen = debugRunning, s.gen+1
	thread := s.thread
	x.async(func(ctx context.Context) error { return c.Continue(ctx, thread) }, nil)
	return nil
}

func (x *debugExt) step(kind dap.StepKind) error {
	s, c := x.sess, x.client()
	if c == nil || s.state != debugPaused {
		return errors.New("not paused")
	}
	s.state, s.gen = debugRunning, s.gen+1
	thread := s.thread
	x.async(func(ctx context.Context) error { return c.Step(ctx, thread, kind) }, nil)
	return nil
}

func (x *debugExt) pause() error {
	s, c := x.sess, x.client()
	if c == nil || s.state != debugRunning {
		return errors.New("not running")
	}
	thread := s.thread
	if thread == 0 {
		thread = 1
	}
	x.async(func(ctx context.Context) error { return c.Pause(ctx, thread) }, nil)
	return nil
}

// stopSession ends the session, then runs then on the loop.
func (x *debugExt) stopSession(then func()) error {
	s := x.sess
	c := x.client()
	if c == nil {
		if then != nil {
			then()
		}
		return nil
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		err := c.Stop(ctx)
		x.h.Do(func() {
			x.ended(s)
			if err != nil {
				x.print("error", err.Error())
			}
			if then != nil {
				then()
			}
		})
	}()
	return nil
}

// async runs fn off the loop; an error goes to the debug console.
func (x *debugExt) async(fn func(ctx context.Context) error, done func()) {
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := fn(ctx)
		x.h.Do(func() {
			if err != nil && !errors.Is(err, dap.ErrClosed) {
				x.print("error", debugErrText(err))
			}
			if done != nil {
				done()
			}
			x.h.Redraw()
		})
	}()
}

// stateOf is a breakpoint's state in the running session, by its place in the file's list.
func (x *debugExt) stateOf(file string, i int) (dap.BreakpointState, bool) {
	c := x.client()
	if c == nil || !x.sess.live() {
		return dap.BreakpointState{}, false
	}
	states := c.Breakpoints(file)
	if i >= len(states) {
		return dap.BreakpointState{}, false
	}
	return states[i], true
}

func (x *debugExt) decorations(ctx registry.DecorationCtx) []registry.Decoration {
	var out []registry.Decoration
	for i, b := range x.bps[ctx.Path] {
		if b.Line < ctx.Top || b.Line >= ctx.Bottom {
			continue
		}
		style := "debug.breakpoint"
		note := b.note()
		if st, ok := x.stateOf(ctx.Path, i); ok {
			if !st.Verified {
				style = "debug.breakpoint.unverified"
				note = strings.TrimPrefix(note+" · "+cmpOr(st.Message, "not set"), " · ")
			}
			if n := x.sess.hits[st.ID]; n > 0 {
				note = strings.TrimPrefix(note+fmt.Sprintf(" · hit %d×", n), " · ")
			}
		}
		out = append(out, registry.Decoration{Kind: registry.Sign, Line: b.Line, Text: b.glyph(), Style: style, Priority: 30})
		if note != "" {
			out = append(out, registry.Decoration{Kind: registry.VirtualText, Line: b.Line, Text: note, Style: "debug.note", Priority: 30})
		}
	}
	if f, ok := x.sess.top(); ok && f.Path == ctx.Path && f.Line >= ctx.Top && f.Line < ctx.Bottom {
		out = append(out,
			registry.Decoration{Kind: registry.Sign, Line: f.Line, Text: "➜", Style: "debug.frame", Priority: 40},
			registry.Decoration{Kind: registry.LineBg, Line: f.Line, Style: "debug.frame.bg", Edge: "debug.frame", Priority: 40})
	}
	return out
}

// counts is "3 breakpoints · 1 conditional · 1 logpoint", for the status line.
func (x *debugExt) counts() string {
	n, cond, logs := 0, 0, 0
	for _, bs := range x.bps {
		for _, b := range bs {
			n++
			switch {
			case b.LogMessage != "":
				logs++
			case b.Condition != "" || b.HitCondition != "":
				cond++
			}
		}
	}
	if n == 0 {
		return ""
	}
	s := fmt.Sprintf("%d breakpoint", n)
	if n > 1 {
		s += "s"
	}
	if cond > 0 {
		s += fmt.Sprintf(" · %d conditional", cond)
	}
	if logs > 0 {
		s += fmt.Sprintf(" · %d logpoint", logs)
		if logs > 1 {
			s += "s"
		}
	}
	return s
}
