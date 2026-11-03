package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/plugin/ossandbox"
)

// fakeEnv makes this test binary a process plugin.
const fakeEnv = "EXT_FAKE_PLUGIN"

func TestMain(m *testing.M) {
	ossandbox.Main()
	if os.Getenv(fakeEnv) != "" {
		fakePlugin()
		return
	}
	os.Exit(m.Run())
}

// fakePlugin answers over stdio: echo returns its params, host asks the host {method, params}, read reads a file
// itself (to see the sandbox), bye sends n notifications and its answer then exits, flood makes n calls to the host
// at once, and it says hello on stderr.
func fakePlugin() {
	fmt.Fprintln(os.Stderr, "fake plugin up")
	var conn *jsonrpc.Conn
	conn = jsonrpc.NewConn(os.Stdin, os.Stdout, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		go func() {
			switch r.Method {
			case "echo":
				r.Reply(r.Params, nil)
			case "host":
				var a struct {
					Method string
					Params json.RawMessage
				}
				json.Unmarshal(r.Params, &a)
				res, err := conn.CallRaw(context.Background(), a.Method, a.Params)
				r.Reply(res, err)
			case "read":
				var a struct{ Path string }
				json.Unmarshal(r.Params, &a)
				b, err := os.ReadFile(a.Path)
				r.Reply(string(b), err)
			case "write":
				var a struct{ Path string }
				json.Unmarshal(r.Params, &a)
				if a.Path == "" {
					a.Path = filepath.Join(os.Getenv(EnvData), "kept")
				}
				r.Reply(a.Path, os.WriteFile(a.Path, []byte("kept"), 0o600))
			case "bye":
				var n int
				json.Unmarshal(r.Params, &n)
				pad := strings.Repeat("x", 4096)
				for i := range n {
					conn.Notify("note", map[string]any{"i": i, "pad": pad})
				}
				r.Reply("bye", nil)
				os.Exit(0)
			case "flood":
				var a struct {
					N      int
					Method string
				}
				json.Unmarshal(r.Params, &a)
				var wg sync.WaitGroup
				for range a.N {
					wg.Go(func() { conn.CallRaw(context.Background(), a.Method, nil) })
				}
				wg.Wait()
				r.Reply(a.N, nil)
			default:
				r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: r.Method})
			}
		}()
	}}, jsonrpc.Options{Version: "2.0"})
	<-conn.Done()
}

type nopReporter struct{}

func (nopReporter) Log() *slog.Logger  { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (nopReporter) Notify(core.Notice) {}

func execLook(name string) (string, error) { return exec.LookPath(name) }

// syncBuf is a log both the test and the process's reader write.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func testProcesses(log *syncBuf) *Processes {
	return &Processes{Methods: HostMethods(nopReporter{}), StopAfter: 200 * time.Millisecond, Log: func(plugin string) *slog.Logger {
		return slog.New(slog.NewTextHandler(log, nil)).With("source", "plugin:"+plugin)
	}}
}

// installed is a plugin directory holding this test binary as its own program.
func installed(t *testing.T) (dir, bin string) {
	t.Helper()
	dir = t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	bin = filepath.Join(dir, "bin", "fake")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, bin
}

func call(t *testing.T, pr *Process, method string, params any) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := pr.Call(ctx, method, params)
	return string(raw), err
}

// A process plugin speaks JSON-RPC over stdio, its stderr goes to its log as lines ("plugin:<id>"), and
// each call it makes to the host goes through the guard: an undeclared one fails naming the capability.
func TestProcessPlugin(t *testing.T) {
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	dir, _ := installed(t)
	work := t.TempDir()
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("in the worktree"), 0o644)
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("s3cret"), 0o644)
	g := NewGrants()
	g.Places = func(string) []string { return []string{work} }
	g.Grant("acme/fake", []Grant{{Cap: CapFSRead, Scope: []string{"worktree"}}})
	var log syncBuf
	ps := testProcesses(&log)
	ps.Places = g.Places
	exited := make(chan error, 1)
	pr, err := ps.Spawn(Caller{ID: "acme/fake", Dir: dir, Guard: g}, "bin/fake", SpawnOpts{Env: []string{fakeEnv + "=1"},
		Exit: func(err error) { exited <- err }})
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Stop()
	if got, err := call(t, pr, "echo", map[string]int{"n": 1}); err != nil || got != `{"n":1}` {
		t.Errorf("echo: %s, %v", got, err)
	}
	if got, err := call(t, pr, "host", map[string]any{"method": "fs.read", "params": map[string]string{"path": filepath.Join(work, "a.txt")}}); err != nil ||
		got != `"in the worktree"` {
		t.Errorf("a declared host call: %s, %v", got, err)
	}
	_, err = call(t, pr, "host", map[string]any{"method": "fs.read", "params": map[string]string{"path": secret}})
	var je *jsonrpc.Error
	if !errors.As(err, &je) || !strings.Contains(je.Message, `"fs.read" capability isn't declared`) {
		t.Errorf("an undeclared host call: %v", err)
	}
	_, err = call(t, pr, "host", map[string]any{"method": "fs.write", "params": map[string]string{"path": filepath.Join(work, "b"), "data": "x"}})
	if err == nil || !strings.Contains(err.Error(), "fs.write") {
		t.Errorf("an undeclared fs.write: %v", err)
	}
	// The OS sandbox: reading the file itself fails too, and the worktree it was granted reads.
	if _, err := call(t, pr, "read", map[string]string{"path": secret}); err == nil {
		t.Error("the process read a file outside its grants")
	}
	if got, err := call(t, pr, "read", map[string]string{"path": filepath.Join(work, "a.txt")}); err != nil || got != `"in the worktree"` {
		t.Errorf("reading its granted worktree: %s, %v", got, err)
	}
	if len(ps.List()) != 1 || ps.List()[0].Plugin != "acme/fake" {
		t.Errorf("running: %+v", ps.List())
	}
	pr.Stop()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("no exit")
	}
	if !strings.Contains(log.String(), "fake plugin up") || !strings.Contains(log.String(), "source=plugin:acme/fake") {
		t.Errorf("its stderr isn't in its log:\n%s", log.String())
	}
	if len(ps.List()) != 0 {
		t.Error("still listed")
	}
}

// A plugin's data directory is its process's to keep things in, found in $ORIEL_PLUGIN_DATA; elsewhere stays shut.
func TestProcessDataDir(t *testing.T) {
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	dir, _ := installed(t)
	data := filepath.Join(t.TempDir(), "plugins", "acme", "fake")
	ps := testProcesses(&syncBuf{})
	pr, err := ps.Spawn(Caller{ID: "acme/fake", Dir: dir, Guard: NewGrants(), Data: data}, "bin/fake", SpawnOpts{Env: []string{fakeEnv + "=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Stop()
	if got, err := call(t, pr, "write", map[string]string{}); err != nil || got != fmt.Sprintf("%q", filepath.Join(data, "kept")) {
		t.Fatalf("write in its data directory: %s, %v", got, err)
	}
	if b, err := os.ReadFile(filepath.Join(data, "kept")); err != nil || string(b) != "kept" {
		t.Errorf("kept: %q %v", b, err)
	}
	if _, err := call(t, pr, "write", map[string]string{"path": filepath.Join(t.TempDir(), "x")}); err == nil {
		t.Error("the process wrote outside its data directory")
	}
}

// "Spawning the plugin's own binary or loading its own .wasm needs no exec grant. Running anything
// else does." A link out of its directory isn't its own.
func TestSpawnNeedsExecForOthers(t *testing.T) {
	tool, err := exec.LookPath("true")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(tool, filepath.Join(dir, "true")); err != nil {
		t.Skip(err)
	}
	g := NewGrants()
	p := Caller{ID: "acme/x", Dir: dir, Guard: g}
	ps := testProcesses(&syncBuf{})
	for _, name := range []string{"true", tool, "./true"} {
		if _, err := ps.Spawn(p, name, SpawnOpts{}); !errors.Is(err, ErrNotDeclared) || !strings.Contains(err.Error(), `"exec"`) {
			t.Errorf("spawn %s with no exec grant: %v", name, err)
		}
	}
	if err := ossandbox.Available(); err != nil {
		t.Skip(err)
	}
	g.Grant("acme/x", []Grant{{Cap: CapExec, Scope: []string{"true"}}})
	pr, err := ps.Spawn(p, tool, SpawnOpts{})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-pr.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("true didn't finish")
	}
	if pr.Err() != nil {
		t.Errorf("true, granted: %v", pr.Err())
	}
}

// Your own config is trusted: its processes run as you, with your environment.
func TestTrustedProcess(t *testing.T) {
	self, _ := os.Executable()
	ps := testProcesses(&syncBuf{})
	pr, err := ps.Spawn(Caller{ID: "init.lua"}, self, SpawnOpts{Env: []string{fakeEnv + "=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Stop()
	if got, err := call(t, pr, "echo", "hi"); err != nil || got != `"hi"` {
		t.Errorf("echo: %s, %v", got, err)
	}
	ps.Close()
	if len(ps.List()) != 0 {
		t.Error("Close left a process")
	}
}

// The OS profile follows the grants: fs scopes, exec commands, and the net hosts' ports.
func TestProfileFor(t *testing.T) {
	g := NewGrants()
	g.Grant("a/b", []Grant{{Cap: CapNet, Scope: []string{"api.linear.app", "hooks.example.com:8443"}},
		{Cap: CapFSWrite, Scope: []string{"worktree"}}, {Cap: CapFSRead, Scope: []string{"/data"}}})
	prof := ProfileFor(Caller{ID: "a/b", Dir: "/plugins/a/b", Guard: g}, func(string) []string { return []string{"/src/w"} })
	if fmt.Sprint(prof.Ports) != "[443 8443]" || fmt.Sprint(prof.Write) != "[/src/w]" || fmt.Sprint(prof.Read) != "[/plugins/a/b /data]" {
		t.Errorf("profile: %+v", prof)
	}
	if p := ProfileFor(Caller{ID: "a/c", Guard: g}, nil); len(p.Ports) != 0 || len(p.Write) != 0 {
		t.Errorf("a plugin with no grants: %+v", p)
	}
}

// A bundled plugin's program is oriel itself, as trusted as oriel: it runs as a trusted process does, outside the OS
// sandbox (a GUI binary can't start under macOS's deny-default profile), while its calls to the host still meet the
// plugin's guard.
func TestBundledProgramRunsAsOriel(t *testing.T) {
	self, _ := os.Executable()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("seen"), 0o600); err != nil {
		t.Fatal(err)
	}
	ps := testProcesses(&syncBuf{})
	defer ps.Close()
	p := Caller{ID: "liamg/linear", Dir: t.TempDir(), Guard: NewGrants(), Programs: map[string][]string{"bin/oriel-linear": {self}}}
	pr, err := ps.Spawn(p, "bin/oriel-linear", SpawnOpts{Env: []string{fakeEnv + "=1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Stop()
	if got, err := call(t, pr, "read", map[string]string{"path": outside}); err != nil || got != `"seen"` {
		t.Errorf("the bundled program was confined: %s, %v", got, err)
	}
}

// What a plugin writes before it exits all arrives: its last notifications and its answer.
func TestProcessExitKeepsItsLastWords(t *testing.T) {
	self, _ := os.Executable()
	for range 5 {
		var mu sync.Mutex
		notes := 0
		ps := testProcesses(&syncBuf{})
		pr, err := ps.Spawn(Caller{ID: "init.lua"}, self, SpawnOpts{Env: []string{fakeEnv + "=1"},
			Notify: func(string, json.RawMessage) (any, bool, error) {
				mu.Lock()
				notes++
				mu.Unlock()
				return nil, true, nil
			}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := call(t, pr, "bye", 200)
		<-pr.Done()
		mu.Lock()
		n := notes
		mu.Unlock()
		if err != nil || got != `"bye"` || n != 200 {
			t.Fatalf("its answer %s, %v; %d notifications of 200", got, err, n)
		}
	}
}

// A plugin's calls to the host run at most maxRequests at a time; the rest wait their turn.
func TestProcessRequestsAreBounded(t *testing.T) {
	self, _ := os.Executable()
	var mu sync.Mutex
	active, most := 0, 0
	release := make(chan struct{})
	ps := testProcesses(&syncBuf{})
	pr, err := ps.Spawn(Caller{ID: "init.lua"}, self, SpawnOpts{Env: []string{fakeEnv + "=1"},
		Request: func(string, json.RawMessage) (any, bool, error) {
			mu.Lock()
			active++
			most = max(most, active)
			mu.Unlock()
			<-release
			mu.Lock()
			active--
			mu.Unlock()
			return nil, true, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Stop()
	done := make(chan error, 1)
	go func() {
		_, err := call(t, pr, "flood", map[string]any{"n": maxRequests + 50, "method": "wait"})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		a := active
		mu.Unlock()
		if a >= maxRequests || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if most != maxRequests {
		t.Errorf("%d requests ran at once, want %d", most, maxRequests)
	}
}
