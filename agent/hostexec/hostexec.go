// Package hostexec runs a provider's processes on this machine, outside any sandbox.
package hostexec

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/term/pty"
)

// Exec is agent.Exec on the host.
type Exec struct{}

var _ agent.Exec = Exec{}

func (Exec) StartPTY(ctx context.Context, cmd *exec.Cmd, cols, rows int) (agent.PTY, error) {
	p, err := pty.Start(cmd, cols, rows)
	if err != nil {
		return nil, err
	}
	t := &term{p: p, done: make(chan struct{})}
	go func() {
		t.err = cmd.Wait()
		close(t.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			killGroup(cmd)
		case <-t.done:
		}
	}()
	return t, nil
}

type term struct {
	p    *pty.PTY
	done chan struct{}
	err  error
	once sync.Once
}

func (t *term) Read(b []byte) (int, error)  { return t.p.Read(b) }
func (t *term) Write(b []byte) (int, error) { return t.p.Write(b) }
func (t *term) Resize(c, r int) error       { return t.p.Resize(c, r) }
func (t *term) Wait() error                 { <-t.done; return t.err }

func (t *term) Close() error {
	t.once.Do(func() { killGroup(t.p.Cmd()) })
	return t.p.Close()
}

func (Exec) Start(ctx context.Context, cmd *exec.Cmd) (agent.Process, error) {
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// The read end is ours, not cmd's: Wait closes a StdoutPipe, which would lose what a quick process wrote.
	out, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = w
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	err = cmd.Start()
	w.Close()
	if err != nil {
		out.Close()
		return nil, err
	}
	p := &proc{cmd: cmd, in: in, out: out, done: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			killGroup(cmd)
		case <-p.done:
		}
	}()
	return p, nil
}

type proc struct {
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  io.ReadCloser
	done chan struct{}
	err  error
}

func (p *proc) Stdin() io.WriteCloser { return p.in }
func (p *proc) Stdout() io.ReadCloser { return p.out }
func (p *proc) Wait() error           { <-p.done; return p.err }
func (p *proc) Kill() error           { killGroup(p.cmd); return nil }

// killGroup ends the process and everything it started: an agent leaves tools running otherwise.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
