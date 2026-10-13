package syntax

import (
	"context"
	"encoding/binary"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// runtime is the one wazero runtime every grammar runs in. Compiled grammars are cached; each parser gets
// its own instance, so its memory is its own.
var runtime struct {
	once     sync.Once
	err      error
	rt       wazero.Runtime
	mu       sync.Mutex
	compiled map[*Language]wazero.CompiledModule
}

func wasmRuntime() (wazero.Runtime, error) {
	runtime.once.Do(func() {
		ctx := context.Background()
		runtime.rt = wazero.NewRuntime(ctx)
		runtime.compiled = map[*Language]wazero.CompiledModule{}
		// The runtime's libc needs WASI; grammars get no files, environment or clock beyond the defaults.
		_, runtime.err = wasi_snapshot_preview1.Instantiate(ctx, runtime.rt)
	})
	return runtime.rt, runtime.err
}

// module is one instance of a grammar's WASM: the tree-sitter runtime, the grammar and shim.c.
type module struct {
	m   api.Module
	fns map[string]api.Function
}

func instantiate(l *Language) (*module, error) {
	rt, err := wasmRuntime()
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	runtime.mu.Lock()
	cm, ok := runtime.compiled[l]
	if !ok {
		cm, err = rt.CompileModule(ctx, l.Wasm)
		if err != nil {
			runtime.mu.Unlock()
			return nil, fmt.Errorf("syntax: compiling %s: %w", l.Name, err)
		}
		runtime.compiled[l] = cm
	}
	runtime.mu.Unlock()
	m, err := rt.InstantiateModule(ctx, cm, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return nil, fmt.Errorf("syntax: starting %s: %w", l.Name, err)
	}
	return &module{m: m, fns: map[string]api.Function{}}, nil
}

func (m *module) call(name string, args ...uint64) uint32 {
	f, ok := m.fns[name]
	if !ok {
		f = m.m.ExportedFunction(name)
		if f == nil {
			panic("syntax: grammar has no " + name)
		}
		m.fns[name] = f
	}
	r, err := f.Call(context.Background(), args...)
	if err != nil {
		panic(fmt.Sprintf("syntax: %s: %v", name, err))
	}
	if len(r) == 0 {
		return 0
	}
	return uint32(r[0])
}

// put copies b into the module's memory and returns where; free it with m.free.
func (m *module) put(b []byte) uint32 {
	p := m.call("alloc", uint64(max(len(b), 1)))
	if !m.m.Memory().Write(p, b) {
		panic("syntax: write out of range")
	}
	return p
}

func (m *module) free(p uint32) { m.call("free", uint64(p)) }

func (m *module) bytes(p, n uint32) []byte {
	b, ok := m.m.Memory().Read(p, n)
	if !ok {
		panic("syntax: read out of range")
	}
	return append([]byte(nil), b...)
}

func (m *module) u32(p uint32) uint32 {
	v, ok := m.m.Memory().ReadUint32Le(p)
	if !ok {
		panic("syntax: read out of range")
	}
	return v
}

func (m *module) u32s(p, n uint32) []uint32 {
	b, ok := m.m.Memory().Read(p, n*4)
	if !ok {
		panic("syntax: read out of range")
	}
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.LittleEndian.Uint32(b[i*4:])
	}
	return out
}

// scratch is a few uint32s of module memory for out-parameters.
func (m *module) scratch(n int) uint32 { return m.call("alloc", uint64(n*4)) }

// str reads a C string a function returned along with its length through an out-parameter.
func (m *module) str(fn string, args ...uint64) string {
	lp := m.scratch(1)
	defer m.free(lp)
	p := m.call(fn, append(args, uint64(lp))...)
	return string(m.bytes(p, m.u32(lp)))
}

func (m *module) close() { m.m.Close(context.Background()) }
