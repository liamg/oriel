package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/internal/procgroup"
	"github.com/liamg/oriel/internal/tailbuf"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name= cap=ui
type Root struct{ a *APIs }

//oriel:api doc="Add a command: name is its id, what :name, the omnibox and oriel.run run; fn gets the command's arguments. opts: desc; ex, an ex command that runs it too, or a list of them (ex = 'Hi' makes :Hi {text} run it), with what's typed after it as args.args, or as the argument ex_arg names, and a ! as args.bang; args, the arguments it takes, each { name, type, required, doc } with type any, string, text, int, float, bool, list or map: a run with a key it doesn't name, a value of the wrong type or a required one missing fails, saying so. Without args it takes anything. user_only = true makes it yours alone to start: your keys, clicks and config run it, and no plugin can through oriel.run, its own included." optional=opts
func (r Root) Command(name string, fn *script.Fn, opts script.Table, where string) error {
	if fn == nil {
		return fmt.Errorf("%s: no function", name)
	}
	spec, err := argSpec(opts["args"])
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	r.a.own(r.a.Host.Command(registry.Command{ID: name, Title: str(opts, "desc"), Ex: exNames(opts["ex"]), ExArg: str(opts, "ex_arg"), Args: spec, Source: where, UserOnly: opts["user_only"] == true,
		Run: func(_ registry.Ctx, args registry.Args) error {
			_, err := r.a.VM.Call(fn, map[string]any(args))
			return err
		}}))
	return nil
}

//oriel:api doc="Run a command by its id, with arguments. A command that does what a capability guards needs that capability: running a program (a terminal, the tests, the debugger) exec, for that program; committing, staging, pushing or raising a pull request git; saving or editing buffers and files editor.write; pasting clipboard; answering an agent's ask gate; starting or stopping a session session.control; prompting an agent agent.prompt. A command its args run after it ({then}) needs what that one needs. One that changes who may do what (installing plugins or accepting their consent, settings, secrets, code hosts, updates, running Lua as your config) is yours alone: a plugin can't run it." optional=args
func (r Root) Run(id string, args script.Table) error {
	if err := r.a.commandGrant(id, registry.Args(args)); err != nil {
		return err
	}
	return r.a.Host.Run(context.Background(), id, registry.Args(args))
}

//oriel:api doc="Hook an event: fn(ev) observes it; returns false, reason to veto it, a changed ev to rewrite it, or a value to decide it; vetoing, rewriting or deciding needs the gate capability. opts: pattern (a glob over the event's file), priority, desc." optional=opts
func (r Root) On(event string, opts any, fn any, where string) error {
	o, f, err := optsAndFn(opts, fn)
	if err != nil {
		return fmt.Errorf("%s: %w", event, err)
	}
	for _, e := range r.a.Host.Events() {
		if e.Name == event && e.Grant != "" {
			if err := r.a.Check(plugin.Capability(e.Grant), ""); err != nil {
				return fmt.Errorf("%s: %w", event, err)
			}
		}
	}
	prio, _ := o["priority"].(float64)
	h := registry.Hook{Event: event, Pattern: str(o, "pattern"), Priority: int(prio), Kind: registry.HookKind(str(o, "kind")), Label: str(o, "desc"),
		Source: where}
	h.Fn = func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		t := toTable(ev)
		out, err := r.a.VM.Call(f, t)
		if e := (*script.Error)(nil); errors.As(err, &e) {
			e.Traceback += "\n\toriel.on: dispatch · " + event
		}
		if err != nil {
			return registry.Verdict{}, err
		}
		v, rewrite := verdict(out)
		if v.Veto || v.Decided || v.Rewrote {
			if err := r.a.Check(plugin.CapGate, event); err != nil {
				return registry.Verdict{}, err // the event goes on as it was
			}
		}
		if v.Rewrote {
			fromTable(rewrite, ev)
		}
		return v, nil
	}
	r.a.own(r.a.Host.Hook(h))
	return nil
}

//oriel:api doc="Stand in for what fn provides, until it's first used, as a lazy plugin does: spec has commands (globs: 'Jira*'), keys ('<leader>f' in normal mode, or 'global <D-j>'), events ('SessionStart', or 'BufRead *.go'), filetypes ('go'), omnibox (prefixes) and views (globs over view ids, for view.open). The first use calls fn, then goes on as if it had all been there; load:<name> calls it now. Takes its spec too: { name, commands, keys, …, run = fn }." optional=spec,fn
func (r Root) Placeholder(first any, spec script.Table, fn *script.Fn, where string) error {
	if t, ok := first.(script.Table); ok { // the spec table alone: its name and run in it
		spec, fn = t, nil
	}
	name, fn, err := specOr(first, fn, "name")
	if err != nil {
		return fmt.Errorf("oriel.placeholder: %w", err)
	}
	lz := plugin.Lazy{Commands: plugin.Strings(spec["commands"]), Events: plugin.Strings(spec["events"]), Keys: plugin.Strings(spec["keys"]),
		Filetypes: plugin.Strings(spec["filetypes"]), Omnibox: plugin.Strings(spec["omnibox"]), Views: plugin.Strings(spec["views"])}
	r.a.own(r.a.Host.Placeholder(lz.Placeholder(r.a.Host, name, where, func() error { _, err := r.a.VM.Call(fn); return err })))
	return nil
}

// verdict is what a Lua hook returned: false and a reason vetoes, a table rewrites the event (the table it's
// rewritten to, applied once the hook may), anything else decides.
func verdict(out []any) (registry.Verdict, script.Table) {
	if len(out) == 0 || out[0] == nil || out[0] == true {
		return registry.Verdict{}, nil
	}
	switch v := out[0].(type) {
	case bool:
		reason := ""
		if len(out) > 1 {
			reason, _ = out[1].(string)
		}
		return registry.Verdict{Veto: true, Reason: reason}, nil
	case script.Table:
		return registry.Verdict{Rewrote: true}, v
	default:
		return registry.Verdict{Decided: true, Value: v}, nil
	}
}

//oriel:api doc="Tell the user something, through the one path every notice takes: level is info, warn, error or question (it waits in the inbox until dismissed)." optional=level
func (r Root) Notify(msg string, lvl string, where string) {
	sev := core.SevInfo
	switch level(lvl) {
	case "warn", "warning":
		sev = core.SevWarning
	case "error":
		sev = core.SevError
	case "question":
		sev = core.SevQuestion
	}
	r.a.Host.Notify(core.Notice{ID: "lua." + where, Severity: sev, Text: msg, Location: where})
}

//oriel:api doc="Write to Oriel's log: level is debug, info, warn or error." optional=level
func (r Root) Log(msg string, lvl string, where string) {
	l := r.a.Host.Log().With("source", where)
	switch level(lvl) {
	case "debug":
		l.Debug(msg)
	case "warn", "warning":
		l.Warn(msg)
	case "error":
		l.Error(msg)
	default:
		l.Info(msg)
	}
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

//oriel:api doc="A file's contents, or nil and why not. ~ is your home; a relative path is in the plugin's (or your config's) directory. A plugin reads its own files freely, anything else with fs.read." returns="string?,string?" cap=target
func (r Root) Read(path string) ([]any, error) {
	p := plugin.RealPath(r.a.Plugin.Path(path)) // checked and read as one path, symlinks followed
	if !r.a.Plugin.Own(p) {
		if err := r.a.Check(plugin.CapFSRead, p); err != nil {
			return nil, err
		}
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return []any{nil, err.Error()}, nil
	}
	return []any{string(b)}, nil
}

//oriel:api doc="One of the plugin's own assets, read-only, or nil and why not: path is under its assets/ directory (oriel.asset('icons/x.svg')), and may not lead out of it. Needs no capability." returns="string?,string?"
func (r Root) Asset(path string) ([]any, error) {
	b, err := plugin.Asset(r.a.Plugin.Dir, path)
	if errors.Is(err, fs.ErrNotExist) {
		return []any{nil, err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	return []any{string(b)}, nil
}

//oriel:api doc="Run a program, argv as a list, and return what it printed, or nil and why it failed (not found, or it failed or timed out). ~ is your home, and a relative program (./tool) is in cwd. opts: cwd, timeout (seconds, 10 by default): a timeout stops all the program started; tail (bytes): keep only the end of what it printed, for a long run. Without tail, output past 16 MiB stops the program and fails the call." optional=opts returns="string?,string?" cap=exec
func (r Root) Exec(argv []string, opts script.Table) ([]any, error) {
	run, err := r.program(argv, opts)
	if missing(err) {
		return []any{nil, err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	out, err := run()
	if err != nil {
		return []any{nil, err.Error()}, nil
	}
	return []any{out}, nil
}

// notFound is a program that isn't there: a failure the caller may expect, not a mistake in the call.
type notFound struct{ err error }

func (e *notFound) Error() string { return e.err.Error() }
func (e *notFound) Unwrap() error { return e.err }

//oriel:api doc="Run a program as oriel.exec does, in the background, without holding up the window: fn(output, err) gets what it printed and why it failed (nil if it didn't), on the loop: a program that isn't there too." optional=opts cap=exec
func (r Root) ExecAsync(argv []string, opts any, fn any) error {
	o, f, err := optsAndFn(opts, fn)
	if err != nil {
		return fmt.Errorf("the output: %w", err)
	}
	run, err := r.program(argv, o)
	if missing(err) {
		why := err.Error()
		r.a.Host.Do(func() { r.a.later(f, nil, why) })
		return nil
	}
	if err != nil {
		return err
	}
	plugin.Go(r.a.Host, "oriel.exec_async "+argv[0], func() {
		out, err := run()
		var msg any
		if err != nil {
			msg = err.Error()
		}
		r.a.Host.Do(func() { r.a.later(f, out, msg) })
	})
	return nil
}

// execWaitDelay is how long a program's output may stay open after it ends, held by a child it left running.
const execWaitDelay = time.Second

// execStderrMax is how much of a program's stderr is kept, the end of it: it only goes into an error message.
const execStderrMax = 64 * 1024

// execOutputMax is the most output a call returns whole: past it the call fails rather than return part of it, and a
// caller wanting only the end of a long run asks for its tail.
const execOutputMax = 16 << 20

var (
	errTimedOut = errors.New("timed out")
	errTooMuch  = errors.New("too much output")
)

// capped passes writes on to w until more than max bytes have come, then calls over and fails.
type capped struct {
	w    io.Writer
	n    int
	max  int
	over func()
}

func (c *capped) Write(p []byte) (int, error) {
	if c.n += len(p); c.n > c.max {
		c.over()
		return 0, errTooMuch
	}
	return c.w.Write(p)
}

// program resolves argv's program once, checks the plugin may run it, and returns what runs that same file.
func (r Root) program(argv []string, opts script.Table) (func() (string, error), error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("no program")
	}
	dir := expand(str(opts, "cwd"))
	prog, err := r.a.Plugin.CheckProgram(argv[0], cmp.Or(dir, mustGetwd()))
	if err != nil {
		return nil, err
	}
	if !strings.ContainsRune(prog, filepath.Separator) {
		if prog, err = exec.LookPath(prog); err != nil {
			return nil, &notFound{err}
		}
	} else if _, err := os.Stat(prog); err != nil {
		return nil, &notFound{err}
	}
	tail, _ := opts["tail"].(float64)
	secs, _ := opts["timeout"].(float64)
	if secs == 0 {
		secs = 10
	}
	return func() (string, error) {
		limit := time.Duration(secs * float64(time.Second))
		// The deadline runs on Oriel's clock, which a test moves on by hand.
		ctx, cancel := context.WithCancelCause(context.Background())
		defer cancel(nil)
		timer := clock.Or(r.a.Host.Clock()).AfterFunc(limit, func() { cancel(errTimedOut) })
		defer timer.Stop()
		cmd := exec.CommandContext(ctx, prog, argv[1:]...)
		cmd.Dir = dir
		// The timeout stops all the program started, and a child it leaves holding its output can't hold up the
		// call past WaitDelay.
		procgroup.Own(cmd)
		cmd.Cancel = func() error { return procgroup.Kill(cmd) }
		cmd.WaitDelay = execWaitDelay
		out, errb := &tailbuf.Buffer{Max: int(tail)}, &tailbuf.Buffer{Max: execStderrMax}
		cmd.Stdout, cmd.Stderr = out, errb
		if tail == 0 {
			cmd.Stdout = &capped{w: out, max: execOutputMax, over: func() { cancel(errTooMuch) }}
		}
		// ErrWaitDelay alone means the program ended well and left a child in the background holding its output.
		if err := cmd.Run(); err != nil && !errors.Is(err, exec.ErrWaitDelay) {
			switch cause := context.Cause(ctx); {
			case errors.Is(cause, errTimedOut):
				err = fmt.Errorf("timed out after %v", limit)
			case errors.Is(cause, errTooMuch):
				return "", fmt.Errorf("%s: output over %d MiB: pass tail to keep only its end", argv[0], execOutputMax>>20)
			}
			return out.String(), fmt.Errorf("%s: %v: %s", argv[0], err, strings.TrimSpace(errb.String()))
		}
		return out.String(), nil
	}, nil
}

//oriel:api doc="The time on Oriel's clock, in seconds since 1970 with a fraction: the window's clock, which a test can fix, so prefer it to os.time for anything shown." returns=number
func (r Root) Now() float64 {
	t := time.Now()
	if u := r.a.Host.UI(); u != nil {
		t = u.Now()
	}
	return float64(t.UnixMicro()) / 1e6
}

//oriel:api doc="Encode a value as JSON text: a table, a string, a number, a boolean." returns=string
func (r Root) JSONEncode(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

//oriel:api doc="Decode JSON text to the value it holds: a table, a string, a number, a boolean, or nil." returns=any
func (r Root) JSONDecode(s string) (any, error) {
	var out any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	return out, nil
}

//oriel:api doc="Replace the status line: fn(w) returns oriel.ui nodes; w has the window's session."
func (r Root) Statusline(fn *script.Fn, where string) error {
	return View(r).Override("app.status-line", fn, where)
}

// mustGetwd is the process's directory, where a program named by a relative path runs without a cwd.
func mustGetwd() string {
	d, _ := os.Getwd()
	return d
}

// argSpec is oriel.command's args option: a list of { name, type, required, doc }; nil when it's absent.
func argSpec(v any) ([]registry.Arg, error) {
	if v == nil {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("args is a list of { name, type, required, doc }, not %T", v)
	}
	spec := []registry.Arg{}
	for i, x := range list {
		t, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("args[%d] is a table { name, type, required, doc }, not %T", i+1, x)
		}
		a := registry.Arg{Name: str(t, "name"), Type: registry.ArgType(str(t, "type")), Doc: str(t, "doc")}
		a.Required, _ = t["required"].(bool)
		if a.Name == "" {
			return nil, fmt.Errorf("args[%d] has no name", i+1)
		}
		if a.Type != "" && !slices.Contains(registry.ArgTypes, a.Type) {
			return nil, fmt.Errorf("args[%d] (%s): no type %q; it's one of %v", i+1, a.Name, a.Type, registry.ArgTypes)
		}
		spec = append(spec, a)
	}
	return spec, nil
}

// exNames are a command's ex names as Lua gives them: one name, or a list.
func exNames(v any) []string {
	if s, ok := v.(string); ok {
		if s == "" {
			return nil
		}
		return []string{s}
	}
	var out []string
	for _, x := range listOf(v) {
		if s, ok := x.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}
