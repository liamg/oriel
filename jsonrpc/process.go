package jsonrpc

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Process is a running command and the connection to it. It's reaped once it exits, whatever becomes of it.
type Process struct {
	cmd    *exec.Cmd
	exited chan struct{}
	err    error

	mu   sync.Mutex
	conn io.ReadWriteCloser
}

// Spawn starts cmd with no connection yet; Connect gives it one. A grandchild holding a pipe open doesn't hold up
// Wait for more than a second.
func Spawn(cmd *exec.Cmd) (*Process, error) {
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = time.Second
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, exited: make(chan struct{})}
	go func() {
		p.err = cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// StartStdio starts cmd talking over its stdin and stdout, its stderr going to stderr.
func StartStdio(cmd *exec.Cmd, stderr io.Writer) (*Process, error) {
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
	p, err := Spawn(cmd)
	inR.Close()
	outW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, err
	}
	p.Connect(&stdio{in: inW, out: outR})
	return p, nil
}

// Connect sets the connection to the process.
func (p *Process) Connect(conn io.ReadWriteCloser) {
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
}

func (p *Process) connection() (io.ReadWriteCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn == nil {
		return nil, errors.New("jsonrpc: the process isn't connected")
	}
	return p.conn, nil
}

func (p *Process) Read(b []byte) (int, error) {
	c, err := p.connection()
	if err != nil {
		return 0, err
	}
	return c.Read(b)
}

func (p *Process) Write(b []byte) (int, error) {
	c, err := p.connection()
	if err != nil {
		return 0, err
	}
	return c.Write(b)
}

// Close closes the connection.
func (p *Process) Close() error {
	c, err := p.connection()
	if err != nil {
		return nil
	}
	return c.Close()
}

// Exited is closed once the process has exited and been reaped.
func (p *Process) Exited() <-chan struct{} { return p.exited }

// Wait returns once the process has exited, with how it exited.
func (p *Process) Wait() error {
	<-p.exited
	return p.err
}

func (p *Process) Kill() error {
	select {
	case <-p.exited:
		return nil
	default:
	}
	return p.cmd.Process.Kill()
}

// Stop kills the process, closes the connection and waits for it.
func (p *Process) Stop() {
	p.Kill()
	p.Close()
	p.Wait()
}

// stdio is a command's stdin and stdout as one connection.
type stdio struct {
	in   *os.File
	out  *os.File
	once sync.Once
}

func (s *stdio) Read(b []byte) (int, error)  { return s.out.Read(b) }
func (s *stdio) Write(b []byte) (int, error) { return s.in.Write(b) }

func (s *stdio) Close() error {
	var err error
	s.once.Do(func() {
		s.in.Close()
		err = s.out.Close()
	})
	return err
}
