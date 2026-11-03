package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/jsonrpc"
)

// CodeNotFound is ACP's error for a resource that isn't there: a file.
const CodeNotFound = -32002

type fileParams struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Limit   int    `json:"limit"`
	Content string `json:"content"`
}

// inside is path, made real, when it's in the session's workspace: the agent reaches nothing else through Oriel.
func (s *session) inside(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "the path must be absolute: " + path}
	}
	root, err := realPath(s.spec.Cwd)
	if err != nil || s.spec.Cwd == "" {
		return "", &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "the session has no workspace"}
	}
	p, err := realPath(path)
	if err != nil {
		return "", err
	}
	if !within(p, root) {
		return "", &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: path + " is outside the session's workspace"}
	}
	return p, nil
}

// realPath is path with its symlinks resolved, as far as it exists.
func realPath(path string) (string, error) {
	path = filepath.Clean(path)
	var rest []string
	for {
		if r, err := filepath.EvalSymlinks(path); err == nil {
			return filepath.Join(append([]string{r}, rest...)...), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return filepath.Join(append([]string{path}, rest...)...), nil
		}
		rest = append([]string{filepath.Base(path)}, rest...)
		path = parent
	}
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *session) readFile(params json.RawMessage) (any, error) {
	var p fileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
	}
	path, err := s.inside(p.Path)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, &jsonrpc.Error{Code: CodeNotFound, Message: "no file " + p.Path}
	}
	if err != nil {
		return nil, err
	}
	content := string(b)
	if p.Line > 0 || p.Limit > 0 {
		lines := gitx.SplitLines(b)
		from := max(p.Line-1, 0)
		from = min(from, len(lines))
		to := len(lines)
		if p.Limit > 0 {
			to = min(from+p.Limit, to)
		}
		content = strings.Join(lines[from:to], "")
	}
	return map[string]any{"content": content}, nil
}

func (s *session) writeFile(params json.RawMessage) (any, error) {
	var p fileParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
	}
	path, err := s.inside(p.Path)
	if err != nil {
		return nil, err
	}
	for _, d := range s.spec.Protect {
		if r, err := realPath(d); err == nil && within(path, r) {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: p.Path + " is in " + d + ", which the agent must never edit"}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	return map[string]any{}, os.WriteFile(path, []byte(p.Content), 0o644)
}

// fileDiff is what turned old into new, as Oriel shows an edit: each changed line, numbered.
func fileDiff(path, old, new string) *core.FileDiff {
	d := &core.FileDiff{Path: path}
	for _, h := range gitx.DiffLines([]byte(old), []byte(new)) {
		for i, l := range h.Old {
			d.Lines = append(d.Lines, core.DiffLine{Op: "-", Old: h.OldStart + i, Text: strings.TrimSuffix(l, "\n")})
			d.Removed++
		}
		for i, l := range h.New {
			d.Lines = append(d.Lines, core.DiffLine{Op: "+", New: h.NewStart + i, Text: strings.TrimSuffix(l, "\n")})
			d.Added++
		}
	}
	return d
}

// terminals are the commands the agent runs through Oriel (terminal/*), in the session's sandbox.
type terminals struct {
	s    *session
	mu   sync.Mutex
	seq  int
	open map[string]*term
}

type term struct {
	p      agent.Process
	stop   context.CancelFunc
	done   chan struct{}
	mu     sync.Mutex
	out    []byte
	limit  int
	cut    bool
	code   *int
	signal *string
}

func (t *term) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.out = append(t.out, b...)
	if t.limit > 0 && len(t.out) > t.limit {
		drop := len(t.out) - t.limit
		for drop < len(t.out) && !utf8.RuneStart(t.out[drop]) {
			drop++
		}
		t.out, t.cut = append([]byte(nil), t.out[drop:]...), true
	}
	return len(b), nil
}

func (t *term) status() map[string]any {
	return map[string]any{"exitCode": t.code, "signal": t.signal}
}

func (ts *terminals) answer(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var p struct {
		ID      string   `json:"terminalId"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Env     []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"env"`
		Cwd   string `json:"cwd"`
		Limit int    `json:"outputByteLimit"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
	}
	if method == "terminal/create" {
		return ts.create(p.Command, p.Args, p.Cwd, p.Limit, func(cmd *exec.Cmd) {
			for _, e := range p.Env {
				cmd.Env = append(cmd.Env, e.Name+"="+e.Value)
			}
		})
	}
	ts.mu.Lock()
	t := ts.open[p.ID]
	if method == "terminal/release" {
		delete(ts.open, p.ID)
	}
	ts.mu.Unlock()
	if t == nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no terminal " + p.ID}
	}
	switch method {
	case "terminal/output":
		t.mu.Lock()
		defer t.mu.Unlock()
		r := map[string]any{"output": string(t.out), "truncated": t.cut}
		select {
		case <-t.done:
			r["exitStatus"] = t.status()
		default:
		}
		return r, nil
	case "terminal/wait_for_exit":
		select {
		case <-t.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		return t.status(), nil
	}
	t.p.Kill()
	if method == "terminal/release" {
		t.stop()
	}
	return map[string]any{}, nil
}

func (ts *terminals) create(command string, args []string, cwd string, limit int, env func(*exec.Cmd)) (any, error) {
	s := ts.s
	cmd := exec.Command(command, args...)
	cmd.Dir = cwd
	if cmd.Dir == "" {
		cmd.Dir = s.spec.Cwd
	}
	cmd.Env = append(os.Environ(), s.spec.Env...)
	env(cmd)
	t := &term{limit: limit, done: make(chan struct{})}
	cmd.Stderr = t
	ctx, stop := context.WithCancel(context.Background())
	p, err := s.ex.Start(ctx, cmd)
	if err != nil {
		stop()
		return nil, fmt.Errorf("%s: %w", command, err)
	}
	p.Stdin().Close()
	t.p, t.stop = p, stop
	ts.mu.Lock()
	ts.seq++
	id := fmt.Sprintf("term-%d", ts.seq)
	ts.open[id] = t
	ts.mu.Unlock()
	go func() {
		buf := make([]byte, 32<<10)
		out := p.Stdout()
		for {
			n, err := out.Read(buf)
			if n > 0 {
				t.Write(buf[:n])
			}
			if err != nil {
				break
			}
		}
		err := p.Wait()
		t.mu.Lock()
		code := 0
		var ee interface{ ExitCode() int }
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		if code >= 0 {
			t.code = &code
		} else {
			sig := "SIGKILL"
			t.signal = &sig
		}
		t.mu.Unlock()
		close(t.done)
	}()
	return map[string]any{"terminalId": id}, nil
}

func (ts *terminals) releaseAll() {
	ts.mu.Lock()
	open := ts.open
	ts.open = map[string]*term{}
	ts.mu.Unlock()
	for _, t := range open {
		t.p.Kill()
		t.stop()
	}
}
