package testrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Request is a run to start.
type Request struct {
	Adapter   Adapter
	Workspace Workspace
	Items     []Item // empty runs everything
	Mode      Mode
	Trigger   string        // what asked: "nearest", "file", "last", "failed", "package", "all", "watch"…
	By        By            // who asked
	Change    string        // a fingerprint of the code being tested (a tree hash), for flaky detection
	Timeout   time.Duration // kill the run after this long; tests still running time out. Zero: no limit
	ID        string        // the run's id; one is made when empty
}

// Exec is a run in progress.
type Exec struct {
	run    *Run
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

// Start runs req's command and streams what it reports to emit, from a goroutine of its own, as it happens.
// Events are folded into the Run that Wait returns. Cancel the context, or call Cancel, to stop it.
func Start(ctx context.Context, req Request, emit func(Event)) (*Exec, error) {
	cmd, err := req.Adapter.Command(req.Workspace, req.Items, req.Mode)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "oriel-testrun-")
	if err != nil {
		return nil, err
	}
	for name, b := range cmd.Files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return nil, err
		}
	}
	expand := func(s string) string { return strings.ReplaceAll(s, RunDir, dir) }
	args := make([]string, len(cmd.Args))
	for i, a := range cmd.Args {
		args[i] = expand(a)
	}
	if len(args) == 0 {
		return nil, errors.New("testrun: empty command")
	}
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, req.Timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = cmd.Dir
	c.Env = append(os.Environ(), "ORIEL_TESTRUN_DIR="+dir)
	for _, e := range cmd.Env {
		c.Env = append(c.Env, expand(e))
	}
	killGroup(c)
	c.WaitDelay = 3 * time.Second
	stdout, err := c.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if cmd.Stderr {
		c.Stderr = c.Stdout // the pipe's writer: one stream, in the order it was written
	}

	id := req.ID
	if id == "" {
		id = NewRunID()
	}
	run := &Run{ID: id, Trigger: req.Trigger, By: req.By, Change: req.Change, Mode: req.Mode.String(),
		Adapter: req.Adapter.Name(), Args: args, Start: time.Now()}
	for _, it := range req.Items {
		run.Scope = append(run.Scope, it.Key())
	}
	if err := c.Start(); err != nil {
		cancel()
		os.RemoveAll(dir)
		return nil, err
	}
	x := &Exec{run: run, cancel: cancel, done: make(chan struct{})}
	var mu sync.Mutex
	send := func(e Event) {
		resolveLoc(cmd.Dir, req.Items, &e)
		mu.Lock()
		run.Apply(e)
		mu.Unlock()
		if emit != nil {
			emit(e)
		}
	}
	go func() {
		defer close(x.done)
		defer os.RemoveAll(dir)
		pr, pw := io.Pipe()
		parsed := make(chan error, 1)
		go func() {
			err := req.Adapter.Parse(pr, send)
			io.Copy(io.Discard, pr)
			parsed <- err
		}()
		var rawBuf bytes.Buffer
		io.Copy(pw, io.TeeReader(stdout, &rawBuf))
		pw.Close()
		perr := <-parsed
		werr := c.Wait()
		mu.Lock()
		run.Raw = rawBuf.String()
		run.Stderr = stderr.String()
		mu.Unlock()
		switch {
		case ctx.Err() == context.DeadlineExceeded:
			run.finishUnfinished(ReasonTimeout, fmt.Sprintf("run timed out after %s", req.Timeout), send)
			run.Status = StatusTimedOut
		case ctx.Err() != nil:
			run.Status = StatusCanceled
		case perr != nil:
			x.err = perr
			run.Status = StatusError
		default:
			run.Status = run.overall(werr)
		}
		if cmd.CoverFile != "" && req.Mode == ModeCoverage {
			if cv, ok := req.Adapter.(Coverer); ok {
				if f, err := os.Open(filepath.Join(dir, cmd.CoverFile)); err == nil {
					run.Coverage, err = cv.Coverage(req.Workspace, f)
					f.Close()
					if err != nil && x.err == nil {
						x.err = err
					}
				}
			}
		}
		run.End = time.Now()
		cancel()
	}()
	return x, nil
}

// Cancel stops the run.
func (x *Exec) Cancel() { x.cancel() }

// Done is closed when the run has finished.
func (x *Exec) Done() <-chan struct{} { return x.done }

// Wait waits for the run to finish and returns it.
func (x *Exec) Wait() (*Run, error) {
	<-x.done
	return x.run, x.err
}

// resolveLoc makes a failure's relative location absolute: against the run's directory, or the directory of the
// test it's for (Go prints names relative to the package).
func resolveLoc(dir string, items []Item, e *Event) {
	if e.Failure == nil || e.Failure.Loc == nil || filepath.IsAbs(e.Failure.Loc.File) {
		return
	}
	f := filepath.FromSlash(e.Failure.Loc.File)
	if p := filepath.Join(dir, f); fileExists(p) {
		e.Failure.Loc.File = p
		return
	}
	for _, it := range items {
		if it.Package != e.Package || it.File == "" {
			continue
		}
		d := it.File
		if it.Kind != KindPackage {
			d = filepath.Dir(d)
		}
		if p := filepath.Join(d, f); fileExists(p) {
			e.Failure.Loc.File = p
			return
		}
	}
}
