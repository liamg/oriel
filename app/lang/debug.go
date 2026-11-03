package lang

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	reglang "github.com/liamg/oriel/registry/lang"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// DebugView is the Debug panel: call stack, breakpoints, variables, watch and the debug console.
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
	gen    int       // moves on each stop and resume, so an answer about an old stop is dropped
	landed int       // moves as frames and variables arrive
	since  time.Time // when it last stopped, or its frame changed: what the stack and scopes load from
	failed error     // why reading the stop's stack or scopes failed
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

// Debug is the debugger: breakpoints in the gutter (conditional, hit counts,
// logpoints), sessions on registered debug adapters (registry/lang.DebugAdapter), stepping, the stack, variables, watch
// expressions and a REPL, in the Debug panel.
type Debug struct {
	kit.Disposer
	StateFile func() string                    // where the breakpoints and watches are kept for the window's directory
	Logger    func(source string) *slog.Logger // an adapter's stderr goes to the log as source
	h         plugin.Host
	statePath string // the file the breakpoints and watches came from: the directory's the window is on

	bps     map[string][]debugBreakpoint // by absolute path
	watches []string
	sess    *debugSession
	console []consoleLine
	work    kit.Work
	writes  kit.Writer // the breakpoints and watches, saved
	mu      sync.Mutex // guards sess.client for the goroutines that use it

	panel  debugPanel
	inline *inlineCache
}

// Manifest loads it on the first use of one of its commands, keys or its panel, or a file an adapter debugs
// opening, where its breakpoints show.
func (x *Debug) Manifest() plugin.Manifest {
	lz := kit.LazyOn(slices.Concat(x.commands(), x.toolCommands()), x.keymaps())
	lz.Views = []string{DebugView}
	for _, s := range dap.Builtins() {
		lz.Filetypes = append(lz.Filetypes, s.Language...)
	}
	return plugin.Manifest{ID: "debug", Lazy: lz}
}

// DebugEntries are the debugger's from the start: its options, events, built-in adapters (under any configured
// before it loads), rail entry and key.
func DebugEntries() *kit.Entries {
	var adapters []registry.Contribution
	for _, s := range dap.Builtins() {
		adapters = append(adapters, reglang.DebugAdapter(s))
	}
	return &kit.Entries{ID: "debug.entries",
		Options: append([]registry.OptionSpec{{Name: InlineValuesOption, Section: "editor", Type: registry.Bool, Default: true,
			Scopes: registry.ScopeUser | registry.ScopeProject, Help: "While the debugger is paused, show the frame's variables after the lines that name them."}},
			accessOptions()...),
		Events: []registry.EventInfo{
			{Name: "DebugStopped", Fired: true, Payload: DebugStopped{}, Doc: "The debuggee stopped: at a breakpoint, after a step, on an exception or a pause."},
			{Name: "DebugEnded", Fired: true, Payload: DebugEnded{}, Doc: "A debug session ended."},
		},
		Contribs: append(adapters, uireg.RailEntry{Name: "debug", Icon: "debug", Title: "Debug", Command: "view.toggle", Args: debugViewArgs(), Scope: uireg.ScopeProjectEntry, Order: 55}),
		Keymaps:  []registry.Keymap{{Mode: "global", Keys: "<D-D>", Command: "view.toggle", Args: debugViewArgs(), Desc: "Debug"}},
	}
}

func (x *Debug) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.bps = map[string][]debugBreakpoint{}
	x.load()
	x.Add(kit.OnDirChanged(h, "the debugger's breakpoints and watches for the directory", func(kit.DirChanged) { x.follow() }))
	x.Add(h.Decorations("debug", uireg.KeyedDecorations{Key: x.decorationKey, Make: x.decorations}))
	x.Add(h.Contribute(registry.GutterAction{Name: "debug.breakpoint", Command: "debug.breakpoint.toggle", Order: 50, When: x.debuggable,
		Source: "builtin:debug"}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.agentTools()
	x.Add(h.View(DebugView, debugView{x}))
	x.Add(h.View("statusline.left.66-debug", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "debug", Fn: x.statusSegment} })))
	return nil
}

func (x *Debug) Deactivate(ctx context.Context) error {
	x.mu.Lock()
	var c *dap.Client
	if x.sess != nil {
		c = x.sess.client
	}
	x.mu.Unlock()
	plugin.Teardown(ctx, func(ctx context.Context) {
		if c != nil {
			sctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			c.Stop(sctx)
			cancel()
		}
		x.work.Stop()
	})
	x.writes.Wait()
	return x.Disposer.Deactivate(ctx)
}

// Wait waits for the work it has running off the loop: the adapter's answers.
func (x *Debug) Wait() { x.work.Wait() }

func debugViewArgs() registry.Args {
	return registry.Args{"id": DebugView, "place": "dock", "edge": "bottom", "size": 300, "title": "Debug"}
}

// client is the running session's client, nil while it starts or once it's gone.
func (x *Debug) client() *dap.Client {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.sess == nil {
		return nil
	}
	return x.sess.client
}

// adapters are the registered adapters as specs, in the order they're tried.
func (x *Debug) adapters() []dap.AdapterSpec {
	var out []dap.AdapterSpec
	for _, a := range reglang.DebugAdapters(x.h) {
		out = append(out, dap.AdapterSpec(a))
	}
	return out
}

// debuggable reports whether an adapter debugs the language of the file at path.
func (x *Debug) debuggable(path string) bool {
	lang, ok := kit.LanguageFor(x.h, path, nil)
	if !ok {
		return false
	}
	return slices.ContainsFunc(x.adapters(), func(s dap.AdapterSpec) bool { return s.Debugs(lang.Name) })
}

// launch starts the launch config args spell out, on the first installed adapter for its type.
func (x *Debug) launch(a registry.Args) error {
	cfg, err := dap.ConfigFromMap(map[string]any(a), editorDir(x.h))
	if err != nil {
		return err
	}
	return x.startConfig(cfg)
}

// startConfig starts cfg on the first installed adapter for its type, ending the session running first.
func (x *Debug) startConfig(cfg dap.LaunchConfig) error {
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
func (x *Debug) start(spec dap.AdapterSpec, cfg dap.LaunchConfig) {
	s := &debugSession{spec: spec, cfg: cfg, state: debugStarting, vars: map[int][]dap.Variable{}, hits: map[int]int{}}
	x.mu.Lock()
	x.sess = s
	x.mu.Unlock()
	x.changed()
	x.console = []consoleLine{{Kind: "console", Text: fmt.Sprintf("%s · %s", spec.Name, cmp.Or(cfg.Name, cfg.Program))}}
	x.panel.watchVals = nil
	x.h.Run(context.Background(), "view.open", debugViewArgs())
	opts := dap.Options{
		Stderr:      &logs.Lines{Log: logTo(x.h, x.Logger, "dap:"+spec.Name)},
		Breakpoints: x.dapBreakpoints(),
		OnEvent:     func(e dap.Event) { x.h.Do(func() { x.event(s, e) }) },
	}
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
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
			x.changed()
		})
		if err != nil {
			return
		}
		<-c.Done()
		x.h.Do(func() { x.ended(s) })
	})
}

func (x *Debug) ended(s *debugSession) {
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
	x.changed()
}

func (x *Debug) print(kind, text string) {
	for _, l := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		x.console = append(x.console, consoleLine{Kind: kind, Text: l})
	}
	if over := len(x.console) - 2000; over > 0 {
		x.console = x.console[over:]
	}
}

// event applies what the adapter said to the session it's about.
func (x *Debug) event(s *debugSession, e dap.Event) {
	defer x.changed()
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
		s.vars, s.since, s.failed = map[int][]dap.Variable{}, kit.WindowNow(x.h), nil
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
func (x *Debug) fetch(s *debugSession) {
	gen, c, thread := s.gen, s.client, s.thread
	x.async(func(ctx context.Context) error {
		frames, err := c.Stack(ctx, thread)
		if err != nil {
			x.failed(s, gen, err)
			return err
		}
		x.h.Do(func() {
			if s.gen != gen {
				return
			}
			s.frames = frames
			s.landed++
			x.frameChanged(s)
			if f, ok := s.top(); ok {
				x.h.Fire(context.Background(), "DebugStopped", s.spec.Name, DebugStopped{Adapter: s.spec.Name, Reason: s.stop.Reason,
					Thread: s.thread, File: f.Path, Line: f.Line + 1})
			}
		})
		return nil
	}, nil)
}

// failed keeps why a stop's stack or scopes couldn't be read, while it's still that stop.
func (x *Debug) failed(s *debugSession, gen int, err error) {
	x.h.Do(func() {
		if s.gen == gen {
			s.failed = err
		}
	})
}

// frameChanged shows the selected frame: its scopes and their variables, the watches, and its line in the editor.
func (x *Debug) frameChanged(s *debugSession) {
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
			x.failed(s, gen, err)
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
			s.landed++
		})
		return nil
	}, nil)
	x.refresh()
}

// refresh evaluates the watches in the selected frame.
func (x *Debug) refresh() {
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
func (x *Debug) expand(ref int) {
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
				s.landed++
			}
		})
		return nil
	}, nil)
}

func (x *Debug) selectFrame(i int) error {
	s := x.sess
	if s == nil || s.state != debugPaused {
		return errors.New("not paused")
	}
	if i < 0 || i >= len(s.frames) {
		return fmt.Errorf("no frame %d", i)
	}
	s.frame = i
	s.scopes, s.since, s.failed = nil, kit.WindowNow(x.h), nil
	x.panel.watchVals = nil
	x.frameChanged(s)
	return nil
}

func (x *Debug) addWatch(expr string) error {
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
func (x *Debug) eval(expr string) error {
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

func (x *Debug) resume() error {
	s, c := x.sess, x.client()
	if c == nil || s.state != debugPaused {
		return errors.New("not paused")
	}
	s.state, s.gen = debugRunning, s.gen+1
	thread := s.thread
	x.async(func(ctx context.Context) error { return c.Continue(ctx, thread) }, nil)
	return nil
}

func (x *Debug) step(kind dap.StepKind) error {
	s, c := x.sess, x.client()
	if c == nil || s.state != debugPaused {
		return errors.New("not paused")
	}
	s.state, s.gen = debugRunning, s.gen+1
	thread := s.thread
	x.async(func(ctx context.Context) error { return c.Step(ctx, thread, kind) }, nil)
	return nil
}

func (x *Debug) pause() error {
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
func (x *Debug) stopSession(then func()) error {
	s := x.sess
	c := x.client()
	if c == nil {
		if then != nil {
			then()
		}
		return nil
	}
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
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
	})
	return nil
}

// async runs fn off the loop; an error goes to the debug console.
func (x *Debug) async(fn func(ctx context.Context) error, done func()) {
	x.work.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err := fn(ctx)
		x.h.Do(func() {
			if err != nil && !errors.Is(err, dap.ErrClosed) {
				x.print("error", debugErrText(err))
			}
			if done != nil {
				done()
			}
			x.changed()
		})
	})
}

// stateOf is a breakpoint's state in the running session, by its place in the file's list.
func (x *Debug) stateOf(file string, i int) (dap.BreakpointState, bool) {
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

// changed says the breakpoints or the session moved on: the decorations are made again, and a frame drawn.
func (x *Debug) changed() { x.h.DecorationsChanged("debug") }

// decorationKey is what the session's part of the decorations is made from, as the adapter's answers land.
func (x *Debug) decorationKey(ctx uireg.DecorationCtx) any {
	type made struct {
		sess         *debugSession
		gen, frame   int
		state        string
		frames, vars int
		landed       int
		hits         int
		inline       bool
		at           string // the frame shown
		line         int
		syntax       bool // the buffer's parsed: inline values go by its names
	}
	k := made{inline: x.inlineOn()}
	_, k.syntax = syntaxOf(x.h, ctx.Buffer)
	if s := x.sess; s != nil {
		k.sess, k.gen, k.frame, k.state, k.frames, k.landed = s, s.gen, s.frame, s.state, len(s.frames), s.landed
		if f, ok := s.top(); ok {
			k.at, k.line = f.Path, f.Line
		}
		for _, vs := range s.vars {
			k.vars += len(vs)
		}
		for _, n := range s.hits {
			k.hits += n
		}
	}
	return k
}

func (x *Debug) decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	var out []uireg.Decoration
	for i, b := range x.bps[ctx.Path] {
		if b.Line < ctx.Top || b.Line >= ctx.Bottom {
			continue
		}
		style := "debug.breakpoint"
		note := b.note()
		if st, ok := x.stateOf(ctx.Path, i); ok {
			if !st.Verified {
				style = "debug.breakpoint.unverified"
				note = strings.TrimPrefix(note+" · "+cmp.Or(st.Message, "not set"), " · ")
			}
			if n := x.sess.hits[st.ID]; n > 0 {
				note = strings.TrimPrefix(note+fmt.Sprintf(" · hit %d×", n), " · ")
			}
		}
		out = append(out, uireg.Decoration{Kind: uireg.Sign, Line: b.Line, Text: b.glyph(), Style: style, Priority: 30})
		if note != "" {
			out = append(out, uireg.Decoration{Kind: uireg.VirtualText, Line: b.Line, Text: note, Style: "debug.note", Priority: 30})
		}
	}
	if f, ok := x.sess.top(); ok && f.Path == ctx.Path && f.Line >= ctx.Top && f.Line < ctx.Bottom {
		out = append(out,
			uireg.Decoration{Kind: uireg.Sign, Line: f.Line, Text: "➜", Style: "debug.frame", Priority: 40},
			uireg.Decoration{Kind: uireg.LineBg, Line: f.Line, Style: "debug.frame.bg", Edge: "debug.frame", Priority: 40})
	}
	return append(out, x.inlineValues(ctx)...)
}

// counts is "3 breakpoints · 1 conditional · 1 logpoint", for the status line.
func (x *Debug) counts() string {
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
