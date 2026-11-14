package lang

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
)

// What the agent may do in the debugger: each is allowed, asked for (through the session's permission layers) or off.
const (
	debugRead        = "read"
	debugEval        = "eval"
	debugBreakpoints = "breakpoints"
	debugStep        = "step"
)

// debugAccess is one of the agent's debugger actions, as its option and its chip.
type debugAccess struct {
	Action, Label, Default, Help string
}

var debugAccesses = []debugAccess{
	{debugRead, "read frames", "allow", "the stack, a frame's variables and their fields"},
	{debugEval, "eval", "allow", "evaluate expressions in a frame, which may call functions"},
	{debugBreakpoints, "set breakpoints", "ask", "set and remove breakpoints"},
	{debugStep, "step/continue", "off", "continue and step the debuggee"},
}

var debugLevels = []string{"allow", "ask", "off"}

var (
	tDebugAllow = theme.Declare("debug.access.allow", "ok")
	tDebugAsk   = theme.Declare("debug.access.ask", "amber")
	tDebugOff   = theme.Declare("debug.access.off", "error")
)

func debugAccessOption(action string) string { return "debug.agent." + action }

// toolCommands set what the agent tools may do.
func (x *Debug) toolCommands() []registry.Command {
	return []registry.Command{{ID: "debug.agent.access",
		Title: "Set what agents may do in the debugger: {action} read, eval, breakpoints or step, to {level} allow, ask or off (the next by default)",
		Run:   func(_ registry.Ctx, a registry.Args) error { return x.setLevel(a.String("action"), a.String("level")) }}}
}

func accessOptions() []registry.OptionSpec {
	var out []registry.OptionSpec
	for _, a := range debugAccesses {
		out = append(out, registry.OptionSpec{Name: debugAccessOption(a.Action), Type: registry.Enum, Values: debugLevels, Default: a.Default,
			Scopes: registry.ScopeUser,
			Help:   "Whether agents may " + a.Help + " in the debugger: allow, ask (a permission card, as for the agent's own tools) or off."})
	}
	return out
}

// level is how far the agent may take an action: allow, ask or off.
func (x *Debug) level(action string) string {
	if s, _ := registry.Option[string](x.h.Options(), debugAccessOption(action), registry.At{}); slices.Contains(debugLevels, s) {
		return s
	}
	return "off"
}

func (x *Debug) setLevel(action, level string) error {
	i := slices.IndexFunc(debugAccesses, func(a debugAccess) bool { return a.Action == action })
	if i < 0 {
		return fmt.Errorf("no debugger action %q: read, eval, breakpoints or step", action)
	}
	if level == "" {
		level = debugLevels[(slices.Index(debugLevels, x.level(action))+1)%len(debugLevels)]
	}
	return x.h.Options().Set(debugAccessOption(action), registry.LayerUser, registry.At{}, level)
}

// gate is a tool's Confirm: granted, refused, or what the user is asked.
func (x *Debug) gate(action string, line func(p map[string]any) string) func(registry.Ctx, map[string]any) (registry.Confirmation, error) {
	return func(_ registry.Ctx, p map[string]any) (registry.Confirmation, error) {
		a := debugAccesses[slices.IndexFunc(debugAccesses, func(a debugAccess) bool { return a.Action == action })]
		switch x.level(action) {
		case "allow":
			return registry.Confirmation{}, registry.Granted
		case "ask":
			l := line(p)
			return registry.Confirmation{Title: "The agent wants to " + l, Lead: "In the debugger: " + a.Label + " is set to ask.", Line: l}, nil
		}
		return registry.Confirmation{}, fmt.Errorf("the user hasn't let agents %s in the debugger (%s is off). Ask them to allow it in the Debug "+
			"panel's agent access, or to do it themselves", a.Help, debugAccessOption(action))
	}
}

// agentTools are the debugger's tools for agents: ordinary agent tools over the running
// session, each gated by the access the user granted.
func (x *Debug) agentTools() {
	for _, c := range x.toolCommands() {
		x.Add(x.h.Command(c))
	}
	fixed := func(s string) func(map[string]any) string { return func(map[string]any) string { return s } }
	for _, t := range []registry.AgentTool{
		{Name: "debug_stack", ReadOnly: true, Reads: "debugger", Confirm: x.gate(debugRead, fixed("read the debugger's stack")), Run: x.toolStack,
			Desc: "Where the debugger is: running or paused and why, the paused thread's stack (frame indexes for debug_frame and debug_eval), and the breakpoints."},
		{Name: "debug_frame", ReadOnly: true, Reads: "debugger", Params: map[string]string{"frame": "number?"},
			Confirm: x.gate(debugRead, fixed("read a frame's variables")), Run: x.toolFrame,
			Desc: "A paused frame's variables, by scope: frame is its index in debug_stack (the frame selected in the Debug panel by default). A structured value has a ref for debug_variables."},
		{Name: "debug_variables", ReadOnly: true, Reads: "debugger", Params: map[string]string{"ref": "number"},
			Confirm: x.gate(debugRead, fixed("read a variable's fields")), Run: x.toolVariables,
			Desc: "The fields or elements of a structured value: its ref from debug_frame or debug_variables."},
		{Name: "debug_eval", Reads: "debugger", Params: map[string]string{"expression": "string", "frame": "number?"},
			Confirm: x.gate(debugEval, func(p map[string]any) string { s, _ := p["expression"].(string); return "evaluate " + s }), Run: x.toolEval,
			Desc: "Evaluate an expression in a paused frame (its index in debug_stack; the selected frame by default), as the debug console does. It may call functions."},
		{Name: "debug_breakpoint", Params: map[string]string{"file": "string", "line": "number", "condition": "string?", "hit": "string?", "log": "string?", "remove": "boolean?"},
			Confirm: x.gate(debugBreakpoints, x.breakpointLine), Run: x.toolBreakpoint,
			Desc: "Set a breakpoint on a line (1-based) of a file (relative to the project being debugged): with a condition, a hit condition (\"== 3\") or a log message ({expressions} in braces) that prints rather than stops. remove clears the line's."},
		{Name: "debug_continue", Confirm: x.gate(debugStep, fixed("continue the debuggee")), Run: x.toolResume(func() error { return x.resume() }),
			Desc: "Continue the paused debuggee, and wait (up to 30s) until it stops again or ends."},
		{Name: "debug_step", InputSchema: json.RawMessage(`{"type":"object","properties":{"kind":{"type":"string","enum":["over","into","out"]}},"required":["kind"]}`),
			Confirm: x.gate(debugStep, func(p map[string]any) string { k, _ := p["kind"].(string); return "step " + k }), Run: x.toolStep,
			Desc: "Step the paused debuggee over the line, into the call or out of the function, and say where it stopped."},
	} {
		t.Source = "builtin:debug"
		x.Add(x.h.Contribute(t))
	}
}

func (x *Debug) breakpointLine(p map[string]any) string {
	file, _ := p["file"].(string)
	verb := "set a breakpoint at"
	if b, _ := p["remove"].(bool); b {
		verb = "remove the breakpoint at"
	}
	l := fmt.Sprintf("%s %s:%d", verb, file, intArg(p["line"]))
	if note := x.agentBreakpoint(p).note(); note != "" && !strings.HasPrefix(verb, "remove") {
		l += " (" + note + ")"
	}
	return l
}

func (x *Debug) agentBreakpoint(p map[string]any) debugBreakpoint {
	str := func(k string) string { s, _ := p[k].(string); return s }
	return debugBreakpoint{Line: intArg(p["line"]) - 1, Condition: str("condition"), HitCondition: str("hit"), LogMessage: str("log")}
}

// where is a frame as the agent reads it: "main.half at main.go:12".
func (x *Debug) where(f dap.Frame) string {
	if f.Path == "" {
		return f.Name
	}
	return fmt.Sprintf("%s at %s:%d", f.Name, agentRel(x.dir(), f.Path), f.Line+1)
}

func (x *Debug) toolStack(ctx registry.Ctx, _ map[string]any) (any, error) {
	var out string
	err := kit.OnLoop(ctx, x.h, func() {
		var b strings.Builder
		s := x.sess
		switch {
		case !s.live():
			b.WriteString("Not debugging: no debug session runs. The user starts one with F5.\n")
		case s.state != debugPaused:
			fmt.Fprintf(&b, "%s: not paused.\n", s.state)
		default:
			fmt.Fprintf(&b, "paused (%s) on thread %d\n", s.stop.Reason, s.thread)
			for i, f := range s.frames {
				sel := ""
				if i == s.frame {
					sel = "  (selected)"
				}
				fmt.Fprintf(&b, "#%d %s%s\n", i, x.where(f), sel)
			}
		}
		if rows := x.bpRows(); len(rows) > 0 {
			b.WriteString("breakpoints:\n")
			for _, r := range rows {
				fmt.Fprintf(&b, "  %s:%d", agentRel(x.dir(), r.file), r.bp.Line+1)
				if n := r.bp.note(); n != "" {
					b.WriteString("  " + n)
				}
				b.WriteString("\n")
			}
		}
		out = strings.TrimSpace(b.String())
	})
	return out, err
}

// paused is the running client and a frame of the paused stack: its index, or the selected one when p has none.
func (x *Debug) paused(ctx registry.Ctx, p map[string]any) (*dap.Client, dap.Frame, error) {
	var (
		c   *dap.Client
		f   dap.Frame
		err error
	)
	lerr := kit.OnLoop(ctx, x.h, func() {
		s := x.sess
		if !s.live() || s.state != debugPaused || s.client == nil {
			err = errors.New("the debuggee isn't paused")
			return
		}
		i := s.frame
		if _, ok := p["frame"]; ok {
			i = intArg(p["frame"])
		}
		if i < 0 || i >= len(s.frames) {
			err = fmt.Errorf("no frame %d: the stack has %d", i, len(s.frames))
			return
		}
		c, f = s.client, s.frames[i]
	})
	if lerr != nil {
		return nil, dap.Frame{}, lerr
	}
	return c, f, err
}

func (x *Debug) writeVars(b *strings.Builder, indent string, vs []dap.Variable) {
	for _, v := range vs {
		fmt.Fprintf(b, "%s%s", indent, v.Name)
		if v.Type != "" {
			fmt.Fprintf(b, " %s", v.Type)
		}
		fmt.Fprintf(b, " = %s", v.Value)
		if v.Ref > 0 {
			fmt.Fprintf(b, "  (ref %d)", v.Ref)
		}
		b.WriteString("\n")
	}
}

func (x *Debug) toolFrame(ctx registry.Ctx, p map[string]any) (any, error) {
	c, f, err := x.paused(ctx, p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	scopes, err := c.Scopes(ctx, f.ID)
	if err != nil {
		return nil, errors.New(debugErrText(err))
	}
	var b strings.Builder
	b.WriteString(x.where(f) + "\n")
	for _, sc := range scopes {
		if sc.Expensive {
			fmt.Fprintf(&b, "%s: (ref %d, not fetched: it's expensive)\n", sc.Name, sc.Ref)
			continue
		}
		vs, err := c.Variables(ctx, sc.Ref)
		if err != nil {
			return nil, errors.New(debugErrText(err))
		}
		b.WriteString(sc.Name + ":\n")
		x.writeVars(&b, "  ", vs)
	}
	return strings.TrimSpace(b.String()), nil
}

func (x *Debug) toolVariables(ctx registry.Ctx, p map[string]any) (any, error) {
	ref := intArg(p["ref"])
	c, _, err := x.paused(ctx, nil)
	if err != nil {
		return nil, err
	}
	if ref <= 0 {
		return nil, errors.New("give a ref from debug_frame")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	vs, err := c.Variables(ctx, ref)
	if err != nil {
		return nil, errors.New(debugErrText(err))
	}
	if len(vs) == 0 {
		return "no fields", nil
	}
	var b strings.Builder
	x.writeVars(&b, "", vs)
	return strings.TrimSpace(b.String()), nil
}

// toolEval evaluates as the debug console does, and shows it there as the agent's.
func (x *Debug) toolEval(ctx registry.Ctx, p map[string]any) (any, error) {
	expr, _ := p["expression"].(string)
	if expr = strings.TrimSpace(expr); expr == "" {
		return nil, errors.New("nothing to evaluate")
	}
	c, f, err := x.paused(ctx, p)
	if err != nil {
		return nil, err
	}
	kit.OnLoop(ctx, x.h, func() { x.print("agent", expr) })
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	v, err := c.Evaluate(ctx, f.ID, expr, dap.ContextREPL)
	if err != nil {
		msg := debugErrText(err)
		x.h.Do(func() { x.print("error", msg) })
		return nil, errors.New(msg)
	}
	x.h.Do(func() { x.print("result", v.Value) })
	out := v.Value
	if v.Type != "" {
		out = v.Type + " " + out
	}
	if v.Ref > 0 {
		out += fmt.Sprintf("  (ref %d)", v.Ref)
	}
	return out, nil
}

func (x *Debug) toolBreakpoint(ctx registry.Ctx, p map[string]any) (any, error) {
	file, _ := p["file"].(string)
	if file == "" {
		return nil, errors.New("no file given")
	}
	path := filepath.Clean(file)
	if !filepath.IsAbs(file) {
		path = filepath.Join(x.dir(), file)
	}
	b := x.agentBreakpoint(p)
	if b.Line < 0 {
		return nil, fmt.Errorf("no line %d", b.Line+1)
	}
	remove, _ := p["remove"].(bool)
	err := kit.OnLoop(ctx, x.h, func() {
		if remove {
			x.remove(path, b.Line)
		} else {
			x.set(path, b)
		}
	})
	if err != nil {
		return nil, err
	}
	what := "set a breakpoint at"
	if remove {
		what = "removed the breakpoint at"
	}
	return registry.Acted{Line: fmt.Sprintf("%s %s:%d", what, agentRel(x.dir(), path), b.Line+1)}, nil
}

func (x *Debug) toolStep(ctx registry.Ctx, p map[string]any) (any, error) {
	kinds := map[string]dap.StepKind{"over": dap.StepOver, "into": dap.StepIn, "in": dap.StepIn, "out": dap.StepOut}
	k, _ := p["kind"].(string)
	kind, ok := kinds[k]
	if !ok {
		return nil, fmt.Errorf("step %q: over, into or out", k)
	}
	return x.toolResume(func() error { return x.step(kind) })(ctx, p)
}

// toolResume runs the debuggee on and waits until it stops again or ends, and says where.
func (x *Debug) toolResume(run func() error) func(registry.Ctx, map[string]any) (any, error) {
	return func(ctx registry.Ctx, _ map[string]any) (any, error) {
		var (
			s   *debugSession
			gen int
			err error
		)
		if lerr := kit.OnLoop(ctx, x.h, func() {
			s = x.sess
			if err = run(); err == nil {
				gen = s.gen
			}
		}); lerr != nil {
			return nil, lerr
		}
		if err != nil {
			return nil, err
		}
		const still = "still running after 30s: debug_stack says where it is once it stops"
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		for {
			var out string
			if err := kit.OnLoop(ctx, x.h, func() {
				switch {
				case s.state == debugEnded:
					out = "the debug session ended"
					if s.exit != nil {
						out += fmt.Sprintf(": the program exited with code %d", *s.exit)
					}
				case s.state == debugPaused && s.gen != gen && len(s.frames) > 0:
					out = fmt.Sprintf("paused (%s) in %s", s.stop.Reason, x.where(s.frames[0]))
				}
			}); errors.Is(err, context.DeadlineExceeded) {
				return still, nil
			} else if err != nil {
				return nil, err
			}
			if out != "" {
				return out, nil
			}
			select {
			case <-ctx.Done():
				return still, nil
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
}
