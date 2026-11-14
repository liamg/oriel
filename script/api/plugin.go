package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/script"
)

// handlerWait is how long a process's request waits for its Lua handler to run on the loop.
const handlerWait = 30 * time.Second

func (a *APIs) methods() *plugin.Methods {
	if a.hostm == nil {
		a.hostm = plugin.HostMethods(a.Host)
	}
	return a.hostm
}

func (a *APIs) processes() *plugin.Processes {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.procs == nil {
		a.procs = &plugin.Processes{Methods: a.methods(), Log: func(id string) *slog.Logger { return plugin.Logger(a.Host, id) }}
		if g, ok := a.Plugin.Guard.(*plugin.Grants); ok {
			a.procs.Places = g.Places
		}
	}
	return a.procs
}

func (a *APIs) wasmHost() *plugin.WASM {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.wasm == nil {
		a.wasm = &plugin.WASM{Methods: a.methods(), Log: func(id string) *slog.Logger { return plugin.Logger(a.Host, id) }}
	}
	return a.wasm
}

// handlers are a process's or module's Lua answers by method: opts.on, and those added later with :on.
type handlers struct {
	mu sync.Mutex
	fn map[string]*script.Fn
}

func newHandlers(on script.Table) *handlers {
	h := &handlers{fn: map[string]*script.Fn{}}
	for k, v := range on {
		if f, ok := v.(*script.Fn); ok {
			h.fn[k] = f
		}
	}
	return h
}

func (h *handlers) get(method string) *script.Fn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.fn[method]
}

func (h *handlers) set(method string, f *script.Fn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if f == nil {
		delete(h.fn, method)
		return
	}
	h.fn[method] = f
}

// call runs a Lua handler with the params decoded, and returns what it returned.
func (a *APIs) call(f *script.Fn, params json.RawMessage) (any, error) {
	var v any
	if len(params) > 0 {
		if err := json.Unmarshal(params, &v); err != nil {
			return nil, err
		}
	}
	out, err := a.VM.Call(f, v)
	if err != nil || len(out) == 0 {
		return nil, err
	}
	return out[0], nil
}

// onLoop runs a Lua handler from another goroutine, on the loop, and waits for it.
func (a *APIs) onLoop(f *script.Fn, params json.RawMessage) (any, error) {
	type answer struct {
		v   any
		err error
	}
	ch := make(chan answer, 1)
	a.Host.Do(func() {
		v, err := a.call(f, params)
		ch <- answer{v, err}
	})
	select {
	case r := <-ch:
		return r.v, r.err
	case <-time.After(handlerWait):
		return nil, errors.New("the plugin's Lua didn't answer")
	}
}

// decode is a JSON value as Lua sees it.
func decode(raw json.RawMessage) any {
	var v any
	json.Unmarshal(raw, &v)
	return v
}

//oriel:api name=process cap=target
type Process struct{ a *APIs }

//oriel:api doc="Start a process plugin that speaks JSON-RPC 2.0 over stdio, a message a line: path is the plugin's own binary (relative to its directory: no grant needed) or a program its exec grant names. A plugin's process runs in an OS sandbox (Landlock, sandbox-exec) holding only what its grants allow; its stderr goes to the log. opts: args, cwd, env = { NAME = value }, on = { method = fn(params) returning the result } answering its requests and notifications (others go to the host: log, notify, fs.read, fs.write), on_exit = fn(err). Returns a process: proc:request(method, params, fn(result, err)), proc:notify(method, params), proc:on(method, fn), proc:pid(), proc:running(), proc:stop(); or nil and why, when the program isn't there." optional=opts returns="table?,string?"
func (p Process) Spawn(path string, opts script.Table) ([]any, error) {
	a := p.a
	h := newHandlers(tableOf(opts["on"]))
	handle := func(method string, params json.RawMessage) (any, bool, error) {
		f := h.get(method)
		if f == nil {
			return nil, false, nil
		}
		v, err := a.onLoop(f, params)
		return v, true, err
	}
	var env []string
	for k, v := range tableOf(opts["env"]) {
		env = append(env, k+"="+fmt.Sprint(v))
	}
	slices.Sort(env)
	o := plugin.SpawnOpts{Args: plugin.Strings(opts["args"]), Dir: expand(str(opts, "cwd")), Env: env, Request: handle, Notify: handle}
	if f, ok := opts["on_exit"].(*script.Fn); ok {
		o.Exit = func(err error) {
			var msg any
			if err != nil {
				msg = err.Error()
			}
			a.Host.Do(func() { a.VM.Call(f, msg) })
		}
	}
	proc, err := a.processes().Spawn(a.Plugin, path, o)
	if missing(err) {
		return []any{nil, err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	return []any{script.Table{
		"request": script.GoFunc(script.Method(func(c *script.Args) ([]any, error) {
			method, params, fn := c.String(0), c.Any(1), c.Fn(2)
			if fn == nil {
				return nil, fmt.Errorf("%s: request needs a function for its answer", method)
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				raw, err := proc.Call(ctx, method, params)
				a.Host.Do(func() {
					if err != nil {
						a.later(fn, nil, err.Error())
						return
					}
					a.later(fn, decode(raw))
				})
			}()
			return nil, nil
		})),
		"notify": script.GoFunc(script.Method(func(c *script.Args) ([]any, error) {
			return nil, proc.Notify(c.String(0), c.Any(1))
		})),
		"on": script.GoFunc(script.Method(func(c *script.Args) ([]any, error) {
			h.set(c.String(0), c.Fn(1))
			return nil, nil
		})),
		"pid": script.GoFunc(script.Method(func(*script.Args) ([]any, error) { return []any{proc.Pid()}, nil })),
		"running": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			select {
			case <-proc.Done():
				return []any{false}, nil
			default:
				return []any{true}, nil
			}
		})),
		"stop": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			go proc.Stop()
			return nil, nil
		})),
	}}, nil
}

//oriel:api name=wasm cap=target
type WASM struct{ a *APIs }

//oriel:api doc="Load a WASM module: path is the plugin's own .wasm (relative to its directory: no grant needed), or a file its fs.read grant covers. It runs in wazero with no way out but the host functions it imports from oriel (request, notify, log), each checked against the plugin's grants. opts: on = { method = fn(params) } answering its requests before the host. Returns a module: mod:call(fn, ...) with numbers, mod:call_json(fn, value) with JSON (both answer at once; a process's proc:request answers in a callback), mod:exports(), mod:close(); or nil and why, when the file isn't there." optional=opts returns="table?,string?"
func (w WASM) Load(path string, opts script.Table) ([]any, error) {
	a := w.a
	h := newHandlers(tableOf(opts["on"]))
	ctx, cancel := watchdog(script.LoadBudget)
	defer cancel()
	mod, err := a.wasmHost().Load(ctx, a.Plugin, path, plugin.WASMOpts{
		Request: func(method string, params json.RawMessage) (any, bool, error) {
			f := h.get(method)
			if f == nil {
				return nil, false, nil
			}
			v, err := a.call(f, params)
			return v, true, err
		}})
	if missing(err) {
		return []any{nil, err.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	return []any{script.Table{
		"call": script.GoFunc(script.Method(func(c *script.Args) ([]any, error) {
			var args []float64
			for i := 1; i < c.Len(); i++ {
				args = append(args, c.Float(i))
			}
			ctx, cancel := watchdog(script.InterruptAfter)
			defer cancel()
			out, err := mod.Call(ctx, c.String(0), args...)
			if err != nil {
				return nil, err
			}
			res := make([]any, len(out))
			for i, v := range out {
				res[i] = v
			}
			return res, nil
		})),
		"call_json": script.GoFunc(script.Method(func(c *script.Args) ([]any, error) {
			ctx, cancel := watchdog(script.InterruptAfter)
			defer cancel()
			raw, err := mod.Request(ctx, c.String(0), c.Any(1))
			if err != nil {
				return nil, err
			}
			return []any{decode(raw)}, nil
		})),
		"exports": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			ex := mod.Exports()
			slices.Sort(ex)
			return []any{ex}, nil
		})),
		"close": script.GoFunc(script.Method(func(*script.Args) ([]any, error) {
			return nil, mod.Close(context.Background())
		})),
	}}, nil
}

// watchdog is a module call's context, stopped past budget as Lua is: InterruptAfter for a call, LoadBudget
// for loading.
func watchdog(budget time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), budget)
}

func tableOf(v any) script.Table {
	t, _ := v.(script.Table)
	return t
}

// missing is a file or program that isn't there: a failure the caller may expect, returned as nil and why.
func missing(err error) bool {
	var nf *notFound
	return err != nil && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) || errors.As(err, &nf))
}
