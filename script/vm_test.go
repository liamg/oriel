package script

import (
	"github.com/liamg/oriel/internal/testhook"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadAndCall(t *testing.T) {
	vm := New()
	defer vm.Close()
	var got *Fn
	var where string
	vm.Bind("", map[string]GoFunc{"keep": func(a *Args) ([]any, error) {
		got, where = a.Fn(0), a.Where()
		return nil, nil
	}})
	err := vm.Load(Chunk{Name: "init.lua", Source: []byte("local L = oriel\n\nL.keep(function(x, t) return x * 2, t.name end)\n")})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || where != "init.lua:3" || got.Source != "init.lua:3" {
		t.Fatalf("fn %v, where %q", got, where)
	}
	out, err := vm.Call(got, 21, Table{"name": "go"})
	if err != nil || len(out) != 2 || out[0] != 42.0 || out[1] != "go" {
		t.Errorf("call: %v, %v", out, err)
	}
	if st := vm.Stats(); len(st) != 1 || st[0].Calls != 1 {
		t.Errorf("stats %+v", st)
	}
}

// A watchdog warns, then interrupts, a script that runs past 50 ms.
func TestWatchdog(t *testing.T) {
	testhook.Set(t, &InterruptAfter, 100*time.Millisecond)
	vm := New()
	defer vm.Close()
	var slow string
	vm.OnSlow = func(src string, _ time.Duration) { slow = src }
	vm.Bind("", map[string]GoFunc{"keep": func(a *Args) ([]any, error) {
		f := a.Fn(0)
		go func() {}()
		_, err := vm.Call(f)
		return nil, err
	}})
	vm.Load(Chunk{Name: "spin.lua", Source: []byte("function spin() while true do end end\nfunction quick() return 1 end")})
	fns, _ := vm.Eval(Chunk{Name: "x", Source: []byte("return spin, quick")})
	start := time.Now()
	_, err := vm.Call(fns[0].(*Fn))
	if err == nil || !strings.Contains(err.Error(), "interrupted") || time.Since(start) > time.Second {
		t.Errorf("a spinning call: %v after %v", err, time.Since(start))
	}
	if slow != "spin.lua:1" {
		t.Errorf("slow call reported for %q", slow)
	}
	if out, err := vm.Call(fns[1].(*Fn)); err != nil || out[0] != 1.0 {
		t.Errorf("the VM after an interrupt: %v, %v", out, err)
	}
}

func TestFutureAndMetatable(t *testing.T) {
	vm := New()
	defer vm.Close()
	vm.Future("ledger", "the review ledger")
	store := map[string]any{}
	vm.Metatable("option", func(_ *Args, k string) (any, error) { return store[k], nil }, func(a *Args, k string, v any) error {
		store[k] = v
		store["at"] = a.Where()
		return nil
	})
	if err := vm.Load(Chunk{Name: "init.lua", Source: []byte("oriel.option.tabstop = 4\nlocal f = oriel.ledger.unread\n")}); err != nil {
		t.Fatal(err)
	}
	if store["tabstop"] != 4.0 || store["at"] != "init.lua:1" {
		t.Errorf("store %v", store)
	}
	_, err := vm.Eval(Chunk{Name: "x", Source: []byte("return oriel.ledger.unread('p')")})
	if err == nil || !strings.Contains(err.Error(), "oriel.ledger.unread isn't available until the review ledger") {
		t.Errorf("calling a future API: %v", err)
	}
	if keys := vm.Keys(""); strings.Join(keys, ",") != "ledger,option" {
		t.Errorf("keys %v", keys)
	}
}

// A call from another goroutine waits for the one running; only a call back from inside it nests.
func TestCallFromAnotherGoroutineWaits(t *testing.T) {
	vm := New()
	defer vm.Close()
	started, release := make(chan struct{}), make(chan struct{})
	var marked atomic.Bool
	vm.Bind("", map[string]GoFunc{
		"block": func(a *Args) ([]any, error) {
			close(started)
			<-release
			return vm.Call(a.Fn(0))
		},
		"mark": func(a *Args) ([]any, error) { marked.Store(true); return nil, nil },
	})
	fns, err := vm.Eval(Chunk{Name: "x", Source: []byte("return function() return oriel.block(function() return 7 end) end, function() oriel.mark() end")})
	if err != nil {
		t.Fatal(err)
	}
	outer := make(chan []any)
	go func() {
		out, _ := vm.Call(fns[0].(*Fn))
		outer <- out
	}()
	<-started
	other := make(chan error)
	go func() {
		_, err := vm.Call(fns[1].(*Fn))
		other <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if marked.Load() {
		t.Error("a call from another goroutine ran while the VM was busy")
	}
	close(release)
	if out := <-outer; len(out) != 1 || out[0] != 7.0 {
		t.Errorf("nested call: %v", out)
	}
	if err := <-other; err != nil || !marked.Load() {
		t.Errorf("waiting call: %v, ran %v", err, marked.Load())
	}
}
