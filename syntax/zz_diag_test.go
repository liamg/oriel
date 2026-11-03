package syntax

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// A temporary diagnostic for TestCompiledFromCacheBudget on CI runners: each stage of opening a grammar from the
// compilation cache, timed on its own. It runs only with ORIEL_DIAG=1.
func TestZZDiagCachedOpenStages(t *testing.T) {
	if os.Getenv("ORIEL_DIAG") == "" {
		t.Skip("diagnostic")
	}
	dir := t.TempDir()
	l, _ := Builtin().Language("go")
	wasm, _, err := l.load()
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile("testdata/highlight/fixture.go")
	t.Logf("cache dir %s, wasm %d bytes, fixture %d bytes", dir, len(wasm), len(src))
	stage := map[string]time.Duration{}
	best := func(name string, d time.Duration) {
		if old, ok := stage[name]; !ok || d < old {
			stage[name] = d
		}
	}
	for round := 0; round < 6; round++ {
		ctx := context.Background()
		t0 := time.Now()
		rt, err := newRuntime(dir)
		if err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		cm, err := rt.CompileModule(ctx, wasm)
		if err != nil {
			t.Fatal(err)
		}
		t2 := time.Now()
		mod, err := rt.InstantiateModule(ctx, cm, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
		if err != nil {
			t.Fatal(err)
		}
		t3 := time.Now()
		m := &module{m: mod, fns: map[string]api.Function{}}
		p := &Parser{lang: l, m: m, parser: m.call("parser_new"), queries: map[string]*query{}, symbols: map[string]uint32{}}
		t4 := time.Now()
		p.Parse(src)
		t5 := time.Now()
		rt.Close(ctx)
		t6 := time.Now()
		if round == 0 {
			t.Logf("round 0 (cold): runtime %v, compile %v, instantiate %v, parser %v, parse %v, close %v",
				t1.Sub(t0), t2.Sub(t1), t3.Sub(t2), t4.Sub(t3), t5.Sub(t4), t6.Sub(t5))
			continue
		}
		best("runtime", t1.Sub(t0))
		best("compile from cache", t2.Sub(t1))
		best("instantiate", t3.Sub(t2))
		best("parser", t4.Sub(t3))
		best("parse", t5.Sub(t4))
		best("close", t6.Sub(t5))
		best("total (compile..parse)", t5.Sub(t1))
	}
	for _, k := range []string{"runtime", "compile from cache", "instantiate", "parser", "parse", "close", "total (compile..parse)"} {
		t.Logf("best of 5 warm: %-24s %v", k, stage[k])
	}
}
