package lsp

import (
	"context"
	"errors"
	"io"
	"os/exec"

	"github.com/liamg/oriel/jsonrpc"
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
	return jsonrpc.StartStdio(cmd, stderr)
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
