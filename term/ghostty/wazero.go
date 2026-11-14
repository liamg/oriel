package ghostty

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed wasm/oriel-vt.wasm.gz
var wasmGz []byte

var runtime struct {
	once     sync.Once
	err      error
	rt       wazero.Runtime
	cm       wazero.CompiledModule
	mu       sync.Mutex
	cacheDir string
	made     bool // the runtime is made: its cache dir is fixed
}

// SetCacheDir keeps the compiled module in dir, so it compiles once per version rather than once per run. Once a
// terminal has been made the runtime is, and another dir is refused.
func SetCacheDir(dir string) error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.made && dir != runtime.cacheDir {
		return fmt.Errorf("ghostty: the terminal runtime is running with the cache %q already, so %q isn't used", runtime.cacheDir, dir)
	}
	runtime.cacheDir = dir
	return nil
}

func compiled() (wazero.Runtime, wazero.CompiledModule, error) {
	runtime.once.Do(func() {
		runtime.mu.Lock()
		dir := runtime.cacheDir
		runtime.made = true
		runtime.mu.Unlock()
		ctx := context.Background()
		cfg := wazero.NewRuntimeConfig()
		if dir != "" {
			cache, err := wazero.NewCompilationCacheWithDir(dir)
			if err != nil {
				runtime.err = err
				return
			}
			cfg = cfg.WithCompilationCache(cache)
		}
		runtime.rt = wazero.NewRuntimeWithConfig(ctx, cfg)
		wasm, err := wasmBytes()
		if err != nil {
			runtime.err = err
			return
		}
		runtime.cm, runtime.err = runtime.rt.CompileModule(ctx, wasm)
	})
	return runtime.rt, runtime.cm, runtime.err
}

func wasmBytes() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(wasmGz))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}

// The shim's Term struct, as the host reads it in wasm32's layout.
const (
	offOut      = 0
	offIn       = 8
	offInCap    = 12
	offReplyLen = 24
	offEvents   = 32
	offHeld     = 36
)

// wasmMachine is a module instance running the shim.
type wasmMachine struct {
	m     api.Module
	mem   api.Memory
	fns   map[string]api.Function
	stack []uint64
	t     uint32 // the shim's Term
}

func newWasm(cols, rows, scrollback int) (*wasmMachine, error) {
	rt, cm, err := compiled()
	if err != nil {
		return nil, err
	}
	m, err := rt.InstantiateModule(context.Background(), cm, wazero.NewModuleConfig().WithName(""))
	if err != nil {
		return nil, err
	}
	w := &wasmMachine{m: m, mem: m.Memory(), fns: map[string]api.Function{}, stack: make([]uint64, 8)}
	w.t = uint32(w.raw("oriel_new", uint64(cols), uint64(rows), uint64(scrollback)))
	if w.t == 0 {
		m.Close(context.Background())
		return nil, errors.New("out of memory")
	}
	return w, nil
}

func (w *wasmMachine) raw(name string, args ...uint64) uint64 {
	f, ok := w.fns[name]
	if !ok {
		f = w.m.ExportedFunction(name)
		if f == nil {
			panic("ghostty: no " + name)
		}
		w.fns[name] = f
	}
	copy(w.stack, args)
	if err := f.CallWithStack(context.Background(), w.stack); err != nil {
		panic(fmt.Sprintf("ghostty: %s: %v", name, err))
	}
	return uint64(uint32(w.stack[0]))
}

func (w *wasmMachine) call(name string, args ...uint64) uint64 {
	if name == "oriel_mark_free" {
		return w.raw(name, args...)
	}
	return w.raw(name, append([]uint64{uint64(w.t)}, args...)...)
}

func (w *wasmMachine) u32(p uint32) uint32 {
	v, ok := w.mem.ReadUint32Le(p)
	if !ok {
		panic("ghostty: read out of range")
	}
	return v
}

func (w *wasmMachine) out(n uint64) []byte {
	b, ok := w.mem.Read(w.u32(w.t+offOut), uint32(n))
	if !ok {
		panic("ghostty: read out of range")
	}
	return b
}

func (w *wasmMachine) in(p []byte) {
	if uint32(len(p)) > w.u32(w.t+offInCap) && w.call("oriel_input", uint64(len(p))) == 0 {
		panic("ghostty: out of memory")
	}
	if !w.mem.Write(w.u32(w.t+offIn), p) {
		panic("ghostty: write out of range")
	}
}

func (w *wasmMachine) replyLen() uint32 { return w.u32(w.t + offReplyLen) }
func (w *wasmMachine) events() uint32   { return w.u32(w.t + offEvents) }

func (w *wasmMachine) held() bool {
	b, _ := w.mem.ReadByte(w.t + offHeld)
	return b != 0
}

func (w *wasmMachine) close() error { return w.m.Close(context.Background()) }
