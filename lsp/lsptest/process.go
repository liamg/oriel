package lsptest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/lsp"
)

// The environment a test binary run as a fake server (ServeProcess) reads: the file it notes its pids in, whether it
// ignores shutdown and exit, and whether it's the server's child.
const (
	processEnv = "ORIEL_LSPTEST_PROCESS"
	stuckEnv   = "ORIEL_LSPTEST_STUCK"
	childEnv   = "ORIEL_LSPTEST_CHILD"
)

// childLife bounds how long a fake server's child lives if nothing kills it, so a leak a test catches doesn't outlive
// the run by much.
const childLife = 2 * time.Minute

// ServeProcess, called first in a test binary's TestMain, turns the binary into a fake language server when a test
// has started it as one (NewProcessServer), and returns at once otherwise. The server starts a child of its own that
// lives until it's killed, as gopls's and tsserver's helpers do; answers initialize and shutdown; and on exit, or
// its stdin closing, ends its child and exits, as a server that behaves does. A stuck one ignores shutdown and exit.
func ServeProcess() {
	if os.Getenv(childEnv) != "" {
		time.Sleep(childLife)
		os.Exit(0)
	}
	file := os.Getenv(processEnv)
	if file == "" {
		return
	}
	child := exec.Command(os.Args[0])
	child.Env = append(os.Environ(), childEnv+"=1")
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "lsptest: starting the child:", err)
		os.Exit(1)
	}
	if err := notePids(file, os.Getpid(), child.Process.Pid); err != nil {
		fmt.Fprintln(os.Stderr, "lsptest:", err)
		os.Exit(1)
	}
	stuck := os.Getenv(stuckEnv) != ""
	quit := func() {
		_ = child.Process.Kill()
		_ = child.Wait()
		os.Exit(0)
	}
	conn := jsonrpc.NewConn(os.Stdin, os.Stdout, jsonrpc.Handlers{
		Request: jsonrpc.Answer(func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
			switch method {
			case "initialize":
				return map[string]any{"capabilities": map[string]any{"textDocumentSync": 1}}, nil
			case "shutdown":
				if stuck {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return nil, nil
			}
			return nil, nil
		}),
		Notify: func(method string, _ json.RawMessage) {
			if method == "exit" && !stuck {
				quit()
			}
		},
	}, jsonrpc.Options{Framing: jsonrpc.Headers, Version: "2.0"})
	<-conn.Done()
	if stuck {
		select {} // until killed
	}
	quit()
}

// notePids appends a server's pid and its child's to file, a line each server.
func notePids(file string, server, child int) error {
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "%d %d\n", server, child); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// ProcessServer is a language server whose command is this test binary run as a fake server (ServeProcess): a real
// process with a child, so a test sees whether stopping it leaves anything running.
type ProcessServer struct {
	Spec lsp.ServerSpec
	file string
}

// NewProcessServer is the server named name for languages, with roots as its root markers; stuck makes it ignore
// shutdown and exit, so only killing it stops it. The test binary's TestMain must call ServeProcess.
func NewProcessServer(t testing.TB, name string, languages, roots []string, stuck bool) *ProcessServer {
	t.Helper()
	file := filepath.Join(t.TempDir(), "pids")
	t.Setenv(processEnv, file)
	if stuck {
		t.Setenv(stuckEnv, "1")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &ProcessServer{file: file,
		Spec: lsp.ServerSpec{Name: name, Language: languages, Cmd: []string{exe}, Roots: roots, Source: "test"}}
}

// Pids are the pids of every server started and of their children, servers first.
func (p *ProcessServer) Pids() (servers, children []int) {
	b, _ := os.ReadFile(p.file)
	for line := range strings.Lines(string(b)) {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		s, _ := strconv.Atoi(f[0])
		c, _ := strconv.Atoi(f[1])
		servers, children = append(servers, s), append(children, c)
	}
	return servers, children
}

// Running are the pids of the servers and children still running.
func (p *ProcessServer) Running() []int {
	servers, children := p.Pids()
	var out []int
	for _, pid := range append(servers, children...) {
		if Alive(pid) {
			out = append(out, pid)
		}
	}
	return out
}
