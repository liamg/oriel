package dap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Process is a running adapter: a connection to it, and a way to wait for it or kill it.
type Process interface {
	io.ReadWriteCloser // reads what the adapter sends, writes to it; Close closes both
	Wait() error       // returns once it has exited
	Kill() error
}

// Launcher starts an adapter in dir. The app's runs it on this machine; tests replay a transcript.
type Launcher func(ctx context.Context, spec AdapterSpec, dir string, stderr io.Writer) (Process, error)

// connectTimeout bounds how long Exec waits for an adapter over TCP to connect or be connectable.
const connectTimeout = 20 * time.Second

// Exec starts the spec's command in dir, its stderr (and stdout, when it talks over TCP) going to stderr, and
// connects to it as its Transport says.
func Exec(ctx context.Context, spec AdapterSpec, dir string, stderr io.Writer) (Process, error) {
	if len(spec.Cmd) == 0 {
		return nil, errors.New("dap: " + spec.Name + " has no command")
	}
	switch spec.Transport {
	case "", TransportStdio:
		return execStdio(spec, dir, stderr)
	case TransportReverse:
		return execReverse(ctx, spec, dir, stderr)
	case TransportTCP:
		return execTCP(ctx, spec, dir, stderr)
	}
	return nil, fmt.Errorf("dap: %s: no transport %q", spec.Name, spec.Transport)
}

func command(spec AdapterSpec, dir string, vars map[string]string) *exec.Cmd {
	args := make([]string, len(spec.Cmd))
	for i, a := range spec.Cmd {
		for k, v := range vars {
			a = strings.ReplaceAll(a, k, v)
		}
		args[i] = a
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second // a grandchild holding a pipe open doesn't hold up Wait
	return cmd
}

func execStdio(spec AdapterSpec, dir string, stderr io.Writer) (Process, error) {
	cmd := command(spec, dir, nil)
	cmd.Stderr = stderr
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
	return &stdioProcess{cmd: cmd, in: inW, out: outR}, nil
}

type stdioProcess struct {
	cmd *exec.Cmd
	in  *os.File
	out *os.File
}

func (p *stdioProcess) Read(b []byte) (int, error)  { return p.out.Read(b) }
func (p *stdioProcess) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *stdioProcess) Kill() error                 { return p.cmd.Process.Kill() }
func (p *stdioProcess) Wait() error                 { return p.cmd.Wait() }

func (p *stdioProcess) Close() error {
	p.in.Close()
	return p.out.Close()
}

// execReverse listens, starts the adapter with the address, and takes the connection it makes.
func execReverse(ctx context.Context, spec AdapterSpec, dir string, stderr io.Writer) (Process, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	cmd := command(spec, dir, map[string]string{"${addr}": ln.Addr().String()})
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	type accepted struct {
		c   net.Conn
		err error
	}
	got := make(chan accepted, 1)
	go func() {
		c, err := ln.Accept()
		got <- accepted{c, err}
	}()
	fail := func(err error) (Process, error) {
		cmd.Process.Kill()
		return nil, err
	}
	select {
	case a := <-got:
		if a.err != nil {
			return fail(a.err)
		}
		return &netProcess{Conn: a.c, cmd: cmd, exited: exited}, nil
	case err := <-exited:
		return nil, fmt.Errorf("dap: %s exited before connecting: %v", spec.Name, err)
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-time.After(connectTimeout):
		return fail(fmt.Errorf("dap: %s didn't connect in %v", spec.Name, connectTimeout))
	}
}

// execTCP starts the adapter on a free port and connects to it once it listens.
func execTCP(ctx context.Context, spec AdapterSpec, dir string, stderr io.Writer) (Process, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()
	cmd := command(spec, dir, map[string]string{"${port}": port})
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.Now().Add(connectTimeout)
	for {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", "127.0.0.1:"+port)
		if err == nil {
			return &netProcess{Conn: c, cmd: cmd, exited: exited}, nil
		}
		select {
		case err := <-exited:
			return nil, fmt.Errorf("dap: %s exited before listening: %v", spec.Name, err)
		case <-ctx.Done():
			cmd.Process.Kill()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			return nil, fmt.Errorf("dap: %s didn't listen on %s in %v", spec.Name, port, connectTimeout)
		}
	}
}

type netProcess struct {
	net.Conn
	cmd    *exec.Cmd
	exited chan error
	once   sync.Once
	err    error
}

func (p *netProcess) Kill() error { return p.cmd.Process.Kill() }

func (p *netProcess) Wait() error {
	p.once.Do(func() { p.err = <-p.exited })
	return p.err
}

// Installed reports whether the spec can run: it connects some other way, its probe succeeds, or its command is on
// PATH.
func Installed(spec AdapterSpec) bool {
	if spec.Connect != nil {
		return true
	}
	if len(spec.Probe) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, spec.Probe[0], spec.Probe[1:]...).Run() == nil
	}
	if len(spec.Cmd) == 0 {
		return false
	}
	_, err := exec.LookPath(spec.Cmd[0])
	return err == nil
}
