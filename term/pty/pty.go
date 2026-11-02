//go:build linux || darwin

package pty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// PTY is the controlling side of a pseudo-terminal with a program running on the other.
type PTY struct {
	f   *os.File
	cmd *exec.Cmd
}

// Start runs cmd with a new pseudo-terminal as its controlling terminal, sized cols × rows. Standard streams the
// command leaves nil are the terminal. The caller waits for the command with cmd.Wait.
func Start(cmd *exec.Cmd, cols, rows int) (*PTY, error) {
	master, slave, err := open()
	if err != nil {
		return nil, err
	}
	defer slave.Close()
	if err := setSize(master, cols, rows); err != nil {
		master.Close()
		return nil, err
	}
	if cmd.Stdin == nil {
		cmd.Stdin = slave
	}
	if cmd.Stdout == nil {
		cmd.Stdout = slave
	}
	if cmd.Stderr == nil {
		cmd.Stderr = slave
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true
	cmd.SysProcAttr.Ctty = 0 // the child's stdin
	if cmd.Stdin != slave {
		cmd.ExtraFiles = append([]*os.File{slave}, cmd.ExtraFiles...)
		cmd.SysProcAttr.Ctty = 3
	}
	if err := cmd.Start(); err != nil {
		master.Close()
		return nil, err
	}
	return &PTY{f: master, cmd: cmd}, nil
}

// Read reads the program's output. Once the program and everything it started have closed the terminal, it
// returns io.EOF.
func (p *PTY) Read(b []byte) (int, error) {
	n, err := p.f.Read(b)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return n, err
}

// Write sends input to the program.
func (p *PTY) Write(b []byte) (int, error) { return p.f.Write(b) }

// Resize sets the terminal's size; the program gets SIGWINCH.
func (p *PTY) Resize(cols, rows int) error { return setSize(p.f, cols, rows) }

// Size is the terminal's size.
func (p *PTY) Size() (cols, rows int, err error) {
	ws, err := unix.IoctlGetWinsize(int(p.f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, err
	}
	return int(ws.Col), int(ws.Row), nil
}

// Cmd is the command Start ran.
func (p *PTY) Cmd() *exec.Cmd { return p.cmd }

// Close closes the terminal; the program gets SIGHUP. On Linux it also interrupts a Read in progress; on macOS that
// Read returns once the program has gone.
func (p *PTY) Close() error {
	p.hangup()
	return p.f.Close()
}

func setSize(f *os.File, cols, rows int) error {
	if cols <= 0 || rows <= 0 || cols > 0xffff || rows > 0xffff {
		return errors.New("pty: bad size")
	}
	return unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(cols), Row: uint16(rows)})
}
