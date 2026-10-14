package worker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/liamg/oriel/wire"
)

// SocketPath is where a machine's worker listens for the app and the CLI: a unix socket only its user can reach.
func SocketPath(home string) string { return filepath.Join(home, "worker.sock") }

// Listen serves the app and the CLI on the worker's socket until ctx ends.
func (w *Worker) Listen(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return fmt.Errorf("a worker is already listening on %s", path)
	}
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return err
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-w.closed:
		}
		ln.Close()
		os.Remove(path)
	}()
	for {
		raw, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		conn, err := wire.Server(raw)
		if err != nil {
			raw.Close()
			continue
		}
		w.Serve(conn)
	}
}

// DialLocal reaches this machine's worker, starting one (oriel worker, detached) if none is listening.
func DialLocal(ctx context.Context, home, orielBin string) (Client, error) {
	path := SocketPath(home)
	if c, err := dial(path); err == nil {
		return c, nil
	}
	if orielBin == "" {
		var err error
		if orielBin, err = os.Executable(); err != nil {
			return Client{}, err
		}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return Client{}, err
	}
	logf, err := os.OpenFile(filepath.Join(home, "worker.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Client{}, err
	}
	defer logf.Close()
	cmd := exec.Command(orielBin, "worker", "--home", home)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Client{}, fmt.Errorf("starting the worker: %w", err)
	}
	go cmd.Wait()
	for {
		if c, err := dial(path); err == nil {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return Client{}, fmt.Errorf("the worker didn't start: see %s", filepath.Join(home, "worker.log"))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func dial(path string) (Client, error) {
	raw, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		return Client{}, err
	}
	conn, err := wire.Client(raw)
	if err != nil {
		raw.Close()
		return Client{}, err
	}
	return Client{Conn: conn}, nil
}

// ServeApp dials an app on another machine and serves it until the connection drops (a remote worker).
func (w *Worker) ServeApp(ctx context.Context, addr, fingerprint string, h wire.Hello) error {
	p, welcome, err := wire.Dial(ctx, addr, fingerprint, h)
	if err != nil {
		if welcome.Reason != "" {
			return fmt.Errorf("%w (%s)", err, welcome.Reason)
		}
		return err
	}
	w.Serve(p.Conn)
	select {
	case <-p.Conn.Done():
		return errors.New("the app hung up")
	case <-ctx.Done():
		p.Conn.Close()
		return nil
	}
}
