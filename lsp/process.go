package lsp

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Process is a running server: its stdio, and a way to wait for it or kill it.
type Process interface {
	io.ReadWriteCloser // reads its stdout, writes its stdin; Close closes both
	Wait() error       // returns once it has exited
	Kill() error
}

// Launcher starts a server for a root. The app's runs it on the worker; tests replay a transcript.
type Launcher func(ctx context.Context, spec ServerSpec, root string, stderr io.Writer) (Process, error)

// Exec starts the spec's command in root, with stderr going to stderr.
func Exec(ctx context.Context, spec ServerSpec, root string, stderr io.Writer) (Process, error) {
	if len(spec.Cmd) == 0 {
		return nil, errors.New("lsp: " + spec.Name + " has no command")
	}
	cmd := exec.Command(spec.Cmd[0], spec.Cmd[1:]...)
	cmd.Dir = root
	cmd.Stderr = stderr
	cmd.WaitDelay = time.Second // a grandchild holding stderr open doesn't hold up Wait
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout = inR, outW
	if err := cmd.Start(); err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	inR.Close()
	outW.Close()
	return &execProcess{cmd: cmd, in: inW, out: outR}, nil
}

type execProcess struct {
	cmd *exec.Cmd
	in  *os.File
	out *os.File
}

func (p *execProcess) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *execProcess) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *execProcess) Kill() error                 { return p.cmd.Process.Kill() }
func (p *execProcess) Wait() error                 { return p.cmd.Wait() }

func (p *execProcess) Close() error {
	p.in.Close()
	return p.out.Close()
}

// Installed reports whether the spec can run: it connects some other way, or its command is on PATH.
func Installed(spec ServerSpec) bool {
	if spec.Connect != nil {
		return true
	}
	if len(spec.Cmd) == 0 {
		return false
	}
	_, err := exec.LookPath(spec.Cmd[0])
	return err == nil
}
