package api

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name= cap=ui
type Root struct{ a *APIs }

//oriel:api doc="Add a command: name is what :Name and the omnibox run, fn gets the command's arguments." optional=opts
func (r Root) Command(name string, fn *script.Fn, opts script.Table, where string) error {
	if fn == nil {
		return fmt.Errorf("%s: no function", name)
	}
	r.a.own(r.a.Host.Command(registry.Command{ID: name, Title: str(opts, "desc"), Source: where,
		Run: func(_ registry.Ctx, args registry.Args) error {
			_, err := r.a.VM.Call(fn, map[string]any(args))
			return err
		}}))
	return nil
}

//oriel:api doc="Run a command by its id, with arguments. A command that does what a capability guards (answering an agent's ask, starting or stopping a session, prompting an agent) needs that capability." optional=args
func (r Root) Run(id string, args script.Table) error {
	if err := r.a.commandGrant(id); err != nil {
		return err
	}
	return r.a.Host.Run(context.Background(), id, registry.Args(args))
}

//oriel:api doc="Hook an event: fn(ev) observes it; returns false, reason to veto it, a changed ev to rewrite it, or a value to decide it. opts: pattern (a glob over the event's file), priority, desc." optional=opts
func (r Root) On(event string, opts any, fn any, where string) error {
	f, ok := opts.(*script.Fn)
	var o script.Table
	if !ok {
		o, _ = opts.(script.Table)
		f, ok = fn.(*script.Fn)
	}
	if !ok {
		return fmt.Errorf("%s: no function", event)
	}
	prio, _ := o["priority"].(float64)
	h := registry.Hook{Event: event, Pattern: str(o, "pattern"), Priority: int(prio), Kind: str(o, "kind"), Label: str(o, "desc"),
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
		v := verdict(out, ev)
		if v.Veto || v.Decided {
			if err := r.a.Check(plugin.CapGate, event); err != nil {
				return registry.Verdict{}, err
			}
		}
		return v, nil
	}
	r.a.own(r.a.Host.Hook(h))
	return nil
}

//oriel:api doc="Stand in for what fn provides, until it's first used, as a lazy plugin does: spec has commands (globs: 'Jira*'), keys ('<leader>f' in normal mode, or 'global <D-j>'), events ('SessionStart', or 'BufRead *.go'), filetypes ('go'), omnibox (prefixes) and views (globs over view ids, for view.open). The first use calls fn, then goes on as if it had all been there; load:<name> calls it now."
func (r Root) Placeholder(name string, spec script.Table, fn *script.Fn, where string) error {
	if fn == nil {
		return fmt.Errorf("%s: no function", name)
	}
	lz := plugin.Lazy{Commands: plugin.Strings(spec["commands"]), Events: plugin.Strings(spec["events"]), Keys: plugin.Strings(spec["keys"]),
		Filetypes: plugin.Strings(spec["filetypes"]), Omnibox: plugin.Strings(spec["omnibox"]), Views: plugin.Strings(spec["views"])}
	r.a.own(r.a.Host.Placeholder(lz.Placeholder(r.a.Host, name, where, func() error { _, err := r.a.VM.Call(fn); return err })))
	return nil
}

// verdict is what a Lua hook returned: false and a reason vetoes, a table rewrites the event, anything else decides.
func verdict(out []any, ev any) registry.Verdict {
	if len(out) == 0 || out[0] == nil || out[0] == true {
		return registry.Verdict{}
	}
	switch v := out[0].(type) {
	case bool:
		reason := ""
		if len(out) > 1 {
			reason, _ = out[1].(string)
		}
		return registry.Verdict{Veto: true, Reason: reason}
	case script.Table:
		fromTable(v, ev)
		return registry.Verdict{Rewrote: true}
	default:
		return registry.Verdict{Decided: true, Value: v}
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

//oriel:api doc="Run a program, argv as a list, and return what it printed, or nil and why it failed (not found, or it failed). ~ is your home, and a relative program (./tool) is in cwd. opts: cwd, timeout (seconds, 10 by default)." optional=opts returns="string?,string?" cap=exec
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
	f, ok := opts.(*script.Fn)
	var o script.Table
	if !ok {
		o, _ = opts.(script.Table)
		f, ok = fn.(*script.Fn)
	}
	if !ok {
		return fmt.Errorf("no function for the output")
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
	secs, _ := opts["timeout"].(float64)
	if secs == 0 {
		secs = 10
	}
	return func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs*float64(time.Second)))
		defer cancel()
		cmd := exec.CommandContext(ctx, prog, argv[1:]...)
		cmd.Dir = dir
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
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
	return View{r.a}.Override("app.statusline", fn, where)
}

// mustGetwd is the process's directory, where a program named by a relative path runs without a cwd.
func mustGetwd() string {
	d, _ := os.Getwd()
	return d
}
