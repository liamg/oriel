// Package wasmrt is a wazero runtime made on first use, with its compiled modules kept. Compiled code is costly
// to make and safe to share, so a package that runs wasm keeps one Runtime for the process, and the process's main
// sets its cache dir once, before any window uses it.
package wasmrt

import (
	"context"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
)

// Runtime is a wazero runtime and the modules compiled in it. The zero value, given a Name, is ready.
type Runtime struct {
	Name string                                      // in errors: "grammar", "terminal"
	Init func(context.Context, wazero.Runtime) error // run once on the new runtime (WASI, say); nil for none

	mu       sync.Mutex
	dir      string
	made     bool // the runtime is made: its cache dir is fixed
	once     sync.Once
	rt       wazero.Runtime
	err      error
	compiled map[[32]byte]wazero.CompiledModule
}

// SetCacheDir keeps compiled modules in dir, so each compiles once per version of it and of wazero, not once per
// run. Once the runtime is made, another dir is refused.
func (r *Runtime) SetCacheDir(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.made && dir != r.dir {
		return fmt.Errorf("the %s runtime is running with the cache %q already, so %q isn't used", r.Name, r.dir, dir)
	}
	r.dir = dir
	return nil
}

// CacheDir is the dir it compiles into, "" for none.
func (r *Runtime) CacheDir() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dir
}

// Get is the runtime, made on the first call.
func (r *Runtime) Get() (wazero.Runtime, error) {
	r.once.Do(func() {
		r.mu.Lock()
		dir := r.dir
		r.made = true
		r.compiled = map[[32]byte]wazero.CompiledModule{}
		r.mu.Unlock()
		r.rt, r.err = r.make(dir)
	})
	return r.rt, r.err
}

func (r *Runtime) make(dir string) (wazero.Runtime, error) {
	ctx := context.Background()
	cfg := wazero.NewRuntimeConfig()
	if dir != "" {
		cache, err := wazero.NewCompilationCacheWithDir(dir)
		if err != nil {
			return nil, err
		}
		cfg = cfg.WithCompilationCache(cache)
	}
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if r.Init != nil {
		if err := r.Init(ctx, rt); err != nil {
			return nil, err
		}
	}
	return rt, nil
}

// Compile is the module keyed by sum, compiled from wasm on its first use.
func (r *Runtime) Compile(sum [32]byte, wasm func() ([]byte, error)) (wazero.Runtime, wazero.CompiledModule, error) {
	rt, err := r.Get()
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if cm, ok := r.compiled[sum]; ok {
		return rt, cm, nil
	}
	b, err := wasm()
	if err != nil {
		return nil, nil, err
	}
	cm, err := rt.CompileModule(context.Background(), b)
	if err != nil {
		return nil, nil, err
	}
	r.compiled[sum] = cm
	return rt, cm, nil
}
