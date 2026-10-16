package plugin

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/core/logs"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// WASMPages is the most memory a module may have, in 64 KiB pages: 64 MiB.
const WASMPages = 1024

// WASM runs WASM plugins in wazero (plugins.md, "Four tiers"). A module has no way out but the host functions
// it imports from "oriel", each checked by the guard, and WASI with no files, environment or sockets (its
// stdout and stderr go to the plugin's log).
//
// The ABI: a module that passes data exports oriel_alloc(len i32) i32, which returns room for len bytes in its
// memory. A function Request calls takes (ptr, len i32) of JSON and returns an i64, the ptr << 32 | len of its
// answer: a JSON-RPC response's {"result": …} or {"error": {"code", "message"}}. The host's functions:
//
//	oriel.request(method_ptr, method_len, params_ptr, params_len i32) i64   a host method; its answer as above
//	oriel.notify(method_ptr, method_len, params_ptr, params_len i32)        a host method, no answer
//	oriel.log(level, ptr, len i32)                                           a line in the plugin's log: 0 debug … 3 error
type WASM struct {
	Methods *Methods                         // the host methods modules may call; none if nil
	Log     func(plugin string) *slog.Logger // where modules' output goes; discarded if nil

	once sync.Once
	rt   wazero.Runtime
	err  error

	mu   sync.Mutex
	n    int
	mods map[string]*Module
}

// Module is one loaded WASM module. Calls into it aren't safe from two goroutines at once.
type Module struct {
	Plugin Caller
	Path   string

	w       *WASM
	mod     api.Module
	request Handler
	out     *logs.Lines
}

// WASMOpts shape a module.
type WASMOpts struct {
	Request Handler // the plugin's own answers to the module's requests, before the host's methods
}

func (w *WASM) runtime(ctx context.Context) (wazero.Runtime, error) {
	w.once.Do(func() {
		cfg := wazero.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(WASMPages)
		w.rt = wazero.NewRuntimeWithConfig(ctx, cfg)
		w.mods = map[string]*Module{}
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, w.rt); err != nil {
			w.err = err
			return
		}
		i32, i64 := api.ValueTypeI32, api.ValueTypeI64
		b := w.rt.NewHostModuleBuilder("oriel")
		b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(w.hostRequest), []api.ValueType{i32, i32, i32, i32},
			[]api.ValueType{i64}).Export("request")
		b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(w.hostNotify), []api.ValueType{i32, i32, i32, i32}, nil).Export("notify")
		b.NewFunctionBuilder().WithGoModuleFunction(api.GoModuleFunc(w.hostLog), []api.ValueType{i32, i32, i32}, nil).Export("log")
		_, w.err = b.Instantiate(ctx)
	})
	return w.rt, w.err
}

// Load compiles and starts a module: the plugin's own .wasm (relative to its directory) needs no grant, any
// other file its fs.read grant. A module's _initialize runs; a command's _start doesn't.
func (w *WASM) Load(ctx context.Context, p Caller, name string, o WASMOpts) (*Module, error) {
	path := p.Path(name)
	if !p.Own(path) {
		if err := p.Check(CapFSRead, RealPath(path)); err != nil {
			return nil, err
		}
	}
	code, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return w.Instantiate(ctx, p, path, code, o)
}

// Instantiate starts a module from its bytes, as Load does once it has read them.
func (w *WASM) Instantiate(ctx context.Context, p Caller, path string, code []byte, o WASMOpts) (*Module, error) {
	rt, err := w.runtime(ctx)
	if err != nil {
		return nil, err
	}
	compiled, err := rt.CompileModule(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	w.mu.Lock()
	w.n++
	name := fmt.Sprintf("%s#%d", p.ID, w.n)
	m := &Module{Plugin: p, Path: path, w: w, request: o.Request}
	w.mods[name] = m
	w.mu.Unlock()
	cfg := wazero.NewModuleConfig().WithName(name).WithStartFunctions("_initialize").WithSysWalltime().WithSysNanotime().
		WithSysNanosleep().WithRandSource(rand.Reader)
	if w.Log != nil {
		m.out = &logs.Lines{Log: w.Log(p.ID)}
		cfg = cfg.WithStdout(m.out).WithStderr(m.out)
	}
	mod, err := rt.InstantiateModule(ctx, compiled, cfg)
	if err != nil {
		w.forget(name)
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.mod = mod
	return m, nil
}

func (w *WASM) forget(name string) {
	w.mu.Lock()
	delete(w.mods, name)
	w.mu.Unlock()
}

// Close closes every module and the runtime.
func (w *WASM) Close(ctx context.Context) error {
	if w.rt == nil {
		return nil
	}
	return w.rt.Close(ctx)
}

func (w *WASM) caller(mod api.Module) *Module {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.mods[mod.Name()]
}

func read(mod api.Module, ptr, n uint64) ([]byte, bool) {
	b, ok := mod.Memory().Read(uint32(ptr), uint32(n))
	if !ok {
		return nil, false
	}
	return append([]byte(nil), b...), true
}

// answer runs a module's call to the host: the plugin's own handler, then the host's methods.
func (m *Module) answer(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if m.request != nil {
		if res, ok, err := m.request(method, params); ok {
			return res, err
		}
	}
	if m.w.Methods == nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no host method " + method}
	}
	return m.w.Methods.Call(ctx, m.Plugin, method, params)
}

func (w *WASM) hostRequest(ctx context.Context, mod api.Module, stack []uint64) {
	m := w.caller(mod)
	method, ok1 := read(mod, stack[0], stack[1])
	params, ok2 := read(mod, stack[2], stack[3])
	stack[0] = 0
	if m == nil || !ok1 || !ok2 {
		return
	}
	res, err := m.answer(ctx, string(method), params)
	b, err := response(res, err)
	if err != nil {
		return
	}
	if packed, err := m.write(ctx, b); err == nil {
		stack[0] = packed
	}
}

func (w *WASM) hostNotify(ctx context.Context, mod api.Module, stack []uint64) {
	m := w.caller(mod)
	method, ok1 := read(mod, stack[0], stack[1])
	params, ok2 := read(mod, stack[2], stack[3])
	if m != nil && ok1 && ok2 {
		m.answer(ctx, string(method), params)
	}
}

func (w *WASM) hostLog(_ context.Context, mod api.Module, stack []uint64) {
	m := w.caller(mod)
	b, ok := read(mod, stack[1], stack[2])
	if m == nil || !ok || w.Log == nil {
		return
	}
	lvl := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError}[min(int(uint32(stack[0])), 3)]
	w.Log(m.Plugin.ID).Log(context.Background(), lvl, string(b))
}

// response is a JSON-RPC response's body: {"result": …} or {"error": …}.
func response(res any, err error) ([]byte, error) {
	if err != nil {
		var e *jsonrpc.Error
		if !errors.As(err, &e) {
			e = &jsonrpc.Error{Code: jsonrpc.InternalError, Message: err.Error()}
		}
		return json.Marshal(map[string]any{"error": e})
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]json.RawMessage{"result": b})
}

// write copies b into the module's memory through its oriel_alloc, and returns where as ptr << 32 | len.
func (m *Module) write(ctx context.Context, b []byte) (uint64, error) {
	alloc := m.mod.ExportedFunction("oriel_alloc")
	if alloc == nil {
		return 0, errors.New("the module doesn't export oriel_alloc")
	}
	out, err := alloc.Call(ctx, uint64(len(b)))
	if err != nil {
		return 0, err
	}
	ptr := uint32(out[0])
	if !m.mod.Memory().Write(ptr, b) {
		return 0, errors.New("oriel_alloc gave memory out of range")
	}
	return uint64(ptr)<<32 | uint64(len(b)), nil
}

// Exports are the names of the functions the module exports.
func (m *Module) Exports() []string {
	var out []string
	for name := range m.mod.ExportedFunctionDefinitions() {
		out = append(out, name)
	}
	return out
}

// Call calls a function of numbers: each argument converted to its parameter's type, each result back.
func (m *Module) Call(ctx context.Context, fn string, args ...float64) ([]float64, error) {
	f := m.mod.ExportedFunction(fn)
	if f == nil {
		return nil, fmt.Errorf("%s exports no function %q", m.Path, fn)
	}
	def := f.Definition()
	if len(args) != len(def.ParamTypes()) {
		return nil, fmt.Errorf("%s takes %d arguments, not %d", fn, len(def.ParamTypes()), len(args))
	}
	in := make([]uint64, len(args))
	for i, t := range def.ParamTypes() {
		switch t {
		case api.ValueTypeI32:
			in[i] = api.EncodeI32(int32(args[i]))
		case api.ValueTypeI64:
			in[i] = api.EncodeI64(int64(args[i]))
		case api.ValueTypeF32:
			in[i] = api.EncodeF32(float32(args[i]))
		case api.ValueTypeF64:
			in[i] = api.EncodeF64(args[i])
		default:
			return nil, fmt.Errorf("%s takes a %s, which isn't a number", fn, api.ValueTypeName(t))
		}
	}
	res, err := f.Call(ctx, in...)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(res))
	for i, t := range def.ResultTypes() {
		switch t {
		case api.ValueTypeI32:
			out[i] = float64(api.DecodeI32(res[i]))
		case api.ValueTypeI64:
			out[i] = float64(int64(res[i]))
		case api.ValueTypeF32:
			out[i] = float64(api.DecodeF32(res[i]))
		case api.ValueTypeF64:
			out[i] = api.DecodeF64(res[i])
		}
	}
	return out, nil
}

// Request calls a function of JSON (the ABI above) with params, and returns its result, or its error as a
// *jsonrpc.Error.
func (m *Module) Request(ctx context.Context, fn string, params any) (json.RawMessage, error) {
	f := m.mod.ExportedFunction(fn)
	if f == nil {
		return nil, fmt.Errorf("%s exports no function %q", m.Path, fn)
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	packed, err := m.write(ctx, b)
	if err != nil {
		return nil, err
	}
	res, err := f.Call(ctx, packed>>32, packed&0xffffffff)
	if err != nil {
		return nil, err
	}
	if len(res) != 1 {
		return nil, fmt.Errorf("%s returns %d values, not one i64", fn, len(res))
	}
	out, ok := read(m.mod, res[0]>>32, res[0]&0xffffffff)
	if !ok {
		return nil, fmt.Errorf("%s answered with memory out of range", fn)
	}
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *jsonrpc.Error  `json:"error"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		return nil, fmt.Errorf("%s: its answer isn't a JSON-RPC response: %v", fn, err)
	}
	if r.Error != nil {
		return nil, r.Error
	}
	if len(r.Result) == 0 {
		return json.RawMessage("null"), nil
	}
	return r.Result, nil
}

// Close stops the module.
func (m *Module) Close(ctx context.Context) error {
	m.w.forget(m.mod.Name())
	if m.out != nil {
		m.out.Close()
	}
	return m.mod.Close(ctx)
}
