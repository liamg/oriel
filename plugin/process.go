package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/plugin/ossandbox"
)

// Handler answers a plugin's request to its own host side (its Lua), before the host's methods: handled false
// passes it on to them.
type Handler func(method string, params json.RawMessage) (result any, handled bool, err error)

// SpawnOpts shape a process plugin.
type SpawnOpts struct {
	Args    []string
	Dir     string          // its working directory: the plugin's own by default
	Env     []string        // added to the environment it gets
	Request Handler         // its requests to the host
	Notify  Handler         // its notifications, the result dropped
	Exit    func(err error) // once it has stopped, with why: nil for a clean exit
	Stderr  io.Writer       // its stderr; the plugin's log ("plugin:<id>") by default
	// Cancel is its protocol's notification that cancels a request, sent for a call whose context ends.
	Cancel *jsonrpc.Cancel
}

// maxRequests is how many of a plugin's calls to the host run at once.
const maxRequests = 64

// Processes runs process plugins: a child speaking JSON-RPC 2.0 over stdio, one
// message per line, each call it makes to the host checked by the guard. A plugin's process is confined by
// ossandbox to what its grants allow; your own config's runs as you, and so does a bundled plugin's (oriel itself).
type Processes struct {
	Methods   *Methods                         // what the processes may call; none if nil
	Log       func(plugin string) *slog.Logger // where stderr goes; discarded if nil
	Places    func(name string) []string       // a named fs scope's directories ("worktree"), for the sandbox
	StopAfter time.Duration                    // how long Stop waits after closing stdin before it kills; 2s if 0

	mu    sync.Mutex
	procs []*Process
}

// Process is one running process plugin.
type Process struct {
	Plugin  string
	Path    string
	Started time.Time

	cmd     *exec.Cmd
	conn    *jsonrpc.Conn
	done    chan struct{}
	err     error
	stopper time.Duration
}

// Spawn starts name, the plugin's own binary (relative to its directory) or a program its exec grant names.
func (ps *Processes) Spawn(p Caller, name string, o SpawnOpts) (*Process, error) {
	path, pre, err := ps.program(p, name)
	if err != nil {
		return nil, err
	}
	// A bundled plugin's program is oriel itself, as trusted as oriel; a GUI binary can't start under macOS's
	// deny-default profile either. Its calls to the host still meet the plugin's guard.
	_, bundled := p.Programs[name]
	args := append(pre, o.Args...)
	var cmd *exec.Cmd
	tmp := ""
	var data []string
	if p.Data != "" {
		if err := os.MkdirAll(p.Data, 0o700); err != nil {
			return nil, err
		}
		data = []string{EnvData + "=" + p.Data}
	}
	if p.Trusted() || bundled {
		cmd = exec.Command(path, args...)
		cmd.Env = append(append(os.Environ(), data...), o.Env...)
	} else {
		if tmp, err = os.MkdirTemp("", "oriel-plugin-"); err != nil {
			return nil, err
		}
		prof := ProfileFor(p, ps.Places)
		prof.Write = append(prof.Write, tmp)
		if p.Data != "" {
			prof.Write = append(prof.Write, RealPath(p.Data))
		}
		if cmd, err = ossandbox.Command(prof, path, args...); err != nil {
			os.RemoveAll(tmp)
			return nil, fmt.Errorf("%s: can't sandbox its process: %w", p.ID, err)
		}
		cmd.Env = append(append(pluginEnv(tmp), data...), o.Env...)
	}
	cmd.Dir = cmpOr(o.Dir, p.Dir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Wait doesn't close a pipe of ours, so the reader gets everything written before the exit.
	stdout, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = w
	stderr := o.Stderr
	var lines *logs.Lines
	if stderr == nil {
		if ps.Log != nil {
			lines = &logs.Lines{Log: ps.Log(p.ID), Level: slog.LevelInfo}
			stderr = lines
		} else {
			stderr = io.Discard
		}
	}
	cmd.Stderr = stderr
	err = cmd.Start()
	w.Close()
	if err != nil {
		stdout.Close()
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		return nil, err
	}
	pr := &Process{Plugin: p.ID, Path: path, Started: time.Now(), cmd: cmd, done: make(chan struct{}), stopper: ps.StopAfter}
	running := make(chan struct{}, maxRequests)
	pr.conn = jsonrpc.NewConn(stdout, stdin, jsonrpc.Handlers{
		Request: func(r *jsonrpc.Request) {
			running <- struct{}{}
			go func() {
				defer func() { <-running }()
				if o.Request != nil {
					if res, ok, err := o.Request(r.Method, r.Params); ok {
						r.Reply(res, err)
						return
					}
				}
				if ps.Methods == nil {
					r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no host method " + r.Method})
					return
				}
				r.Reply(ps.Methods.Call(context.Background(), p, r.Method, r.Params))
			}()
		},
		Notify: func(method string, params json.RawMessage) {
			if o.Notify != nil {
				if _, ok, _ := o.Notify(method, params); ok {
					return
				}
			}
			if ps.Methods != nil {
				ps.Methods.Call(context.Background(), p, method, params)
			}
		},
	}, jsonrpc.Options{Version: "2.0", Cancel: o.Cancel})
	ps.mu.Lock()
	ps.procs = append(ps.procs, pr)
	ps.mu.Unlock()
	go func() {
		err := cmd.Wait()
		select {
		case <-pr.conn.Done():
		case <-time.After(pr.stopAfter()):
		}
		pr.conn.Close()
		if lines != nil {
			lines.Close()
		}
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		pr.err = err
		ps.mu.Lock()
		ps.procs = slices.DeleteFunc(ps.procs, func(x *Process) bool { return x == pr })
		ps.mu.Unlock()
		close(pr.done)
		if o.Exit != nil {
			o.Exit(err)
		}
	}()
	return pr, nil
}

// program is the path of what p asked to run, and the arguments it goes before p's: its own file or one of its
// Programs needs no grant, anything else its exec grant.
func (ps *Processes) program(p Caller, name string) (string, []string, error) {
	if name == "" {
		return "", nil, errors.New("no program")
	}
	if argv, ok := p.Programs[name]; ok && len(argv) > 0 {
		return argv[0], slices.Clone(argv[1:]), nil
	}
	path, err := ps.file(p, name)
	return path, nil, err
}

func (ps *Processes) file(p Caller, name string) (string, error) {
	var path string
	if own := p.Path(name); p.Dir != "" && !filepath.IsAbs(Expand(name)) && fileExists(own) {
		if p.Own(own) {
			return own, nil
		}
		path = own
	} else {
		var err error
		if path, err = exec.LookPath(Expand(name)); err != nil {
			return "", err
		}
		if path, err = filepath.Abs(path); err != nil {
			return "", err
		}
	}
	path = RealPath(path)
	if err := p.Check(CapExec, path); err != nil {
		return "", err
	}
	return path, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// pluginEnv is what a sandboxed process gets of an environment: a PATH, its own temp dir as home, the locale. The
// user's own variables (tokens among them) stay out.
func pluginEnv(tmp string) []string {
	env := []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + tmp, "TMPDIR=" + tmp}
	for _, k := range []string{"LANG", "LC_ALL", "TZ"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// ProfileFor is the OS sandbox a plugin's process gets: its own directory to read, its fs grants, the programs its
// exec grant names, and the TCP ports of its net grant's hosts (443 for a host with none).
func ProfileFor(p Caller, places func(string) []string) ossandbox.Profile {
	var prof ossandbox.Profile
	if p.Dir != "" {
		prof.Read = append(prof.Read, RealPath(p.Dir))
	}
	if p.Guard == nil {
		return prof
	}
	resolve := func(s string) []string {
		if s == "worktree" || s == "config" {
			if places == nil {
				return nil
			}
			return places(s)
		}
		return []string{Expand(s)}
	}
	for _, g := range p.Guard.Declared(p.ID) {
		for _, s := range g.Scope {
			switch g.Cap {
			case CapFSRead:
				prof.Read = append(prof.Read, resolve(s)...)
			case CapFSWrite:
				prof.Write = append(prof.Write, resolve(s)...)
			case CapExec:
				if path, err := exec.LookPath(Expand(s)); err == nil {
					prof.Exec = append(prof.Exec, path)
				}
			case CapNet:
				port := 443
				if _, ps, err := net.SplitHostPort(s); err == nil {
					if n, err := strconv.Atoi(ps); err == nil {
						port = n
					}
				}
				if !slices.Contains(prof.Ports, port) {
					prof.Ports = append(prof.Ports, port)
				}
			}
		}
	}
	return prof
}

// List is every process running, oldest first.
func (ps *Processes) List() []*Process {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return slices.Clone(ps.procs)
}

// Close stops every process.
func (ps *Processes) Close() {
	var wg sync.WaitGroup
	for _, pr := range ps.List() {
		wg.Add(1)
		go func() { defer wg.Done(); pr.Stop() }()
	}
	wg.Wait()
}

// Call sends a request and waits for its result.
func (pr *Process) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return pr.conn.CallRaw(ctx, method, params)
}

// Notify sends a notification.
func (pr *Process) Notify(method string, params any) error { return pr.conn.Notify(method, params) }

// Pid is its process id.
func (pr *Process) Pid() int { return pr.cmd.Process.Pid }

// Done is closed once it has stopped.
func (pr *Process) Done() <-chan struct{} { return pr.done }

// Err is why it stopped, once Done: nil for a clean exit.
func (pr *Process) Err() error {
	select {
	case <-pr.done:
		return pr.err
	default:
		return nil
	}
}

// Stop closes its stdin, which a plugin takes as the end, and kills it if it hasn't gone a little later.
func (pr *Process) Stop() error {
	pr.conn.Close()
	select {
	case <-pr.done:
	case <-time.After(pr.stopAfter()):
		pr.cmd.Process.Kill()
		<-pr.done
	}
	return nil
}

func (pr *Process) stopAfter() time.Duration {
	if pr.stopper == 0 {
		return 2 * time.Second
	}
	return pr.stopper
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
