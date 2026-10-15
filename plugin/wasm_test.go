package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/plugin/hosttest"
)

// PL-2, security.md → Enforcement by tier: WASM has "only host functions; each checks the grant". A module's
// call to the host goes through the guard, and an undeclared one is an error naming the capability.
func TestWASMPlugin(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "m.wasm"), hosttest.Module(), 0o644)
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("hello"), 0o644)
	g := NewGrants()
	g.Grant("acme/w", []Grant{{Cap: CapFSRead, Scope: []string{work}}})
	w := &WASM{Methods: HostMethods(nopReporter{})}
	ctx := context.Background()
	defer w.Close(ctx)
	p := Caller{ID: "acme/w", Dir: dir, Guard: g}
	m, err := w.Load(ctx, p, "m.wasm", WASMOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m.Call(ctx, "add", 2, 40); err != nil || len(got) != 1 || got[0] != 42 {
		t.Errorf("add: %v, %v", got, err)
	}
	if got, err := m.Request(ctx, "echo", map[string]any{"result": []int{1, 2}}); err != nil || string(got) != "[1,2]" {
		t.Errorf("echo: %s, %v", got, err)
	}
	if got, err := m.Request(ctx, "ask", map[string]string{"path": filepath.Join(work, "a.txt")}); err != nil || string(got) != `"hello"` {
		t.Errorf("a declared host call: %s, %v", got, err)
	}
	_, err = m.Request(ctx, "ask", map[string]string{"path": "/etc/hostname"})
	var je *jsonrpc.Error
	if !errors.As(err, &je) || je.Code != CodeNotDeclared || !strings.Contains(je.Message, `"fs.read" capability isn't declared`) {
		t.Errorf("an undeclared host call: %v", err)
	}
	// The plugin's own Lua answers first.
	own, err := w.Load(ctx, p, "m.wasm", WASMOpts{Request: func(method string, params json.RawMessage) (any, bool, error) {
		return "from lua: " + method, true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := own.Request(ctx, "ask", map[string]string{"path": "/etc/hostname"}); err != nil || string(got) != `"from lua: fs.read"` {
		t.Errorf("the plugin's own handler: %s, %v", got, err)
	}
	tctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err := m.Call(tctx, "spin"); err == nil {
		t.Error("a module that never returns wasn't stopped")
	}
	if _, err := m.Call(ctx, "nope"); err == nil {
		t.Error("a function it doesn't export")
	}
}

// Loading a .wasm from outside the plugin's directory needs fs.read for it.
func TestWASMLoadNeedsFSReadOutside(t *testing.T) {
	elsewhere := t.TempDir()
	os.WriteFile(filepath.Join(elsewhere, "m.wasm"), hosttest.Module(), 0o644)
	w := &WASM{}
	defer w.Close(context.Background())
	g := NewGrants()
	p := Caller{ID: "acme/w", Dir: t.TempDir(), Guard: g}
	if _, err := w.Load(context.Background(), p, filepath.Join(elsewhere, "m.wasm"), WASMOpts{}); !errors.Is(err, ErrNotDeclared) {
		t.Errorf("a .wasm outside its directory: %v", err)
	}
	g.Grant("acme/w", []Grant{{Cap: CapFSRead, Scope: []string{elsewhere}}})
	if _, err := w.Load(context.Background(), p, filepath.Join(elsewhere, "m.wasm"), WASMOpts{}); err != nil {
		t.Errorf("with fs.read: %v", err)
	}
}
