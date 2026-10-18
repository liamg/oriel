package cli

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// ErrRunning is a second Oriel on the same home: its sessions are the first's.
var ErrRunning = errors.New("Oriel is already running")

// held is the lock lock took, kept for the life of the process.
var held *os.File

// lock takes ~/.oriel/oriel.lock for this process, so only one Oriel runs on a home. The OS lets it go when the
// process ends, however it ends.
func lock(home string) (*os.File, error) {
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(home, "oriel.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrRunning
		}
		return nil, err
	}
	return f, nil
}

// unlock lets the lock go before the process ends.
func unlock() {
	if held != nil {
		held.Close()
		held = nil
	}
}

// oldWorker refuses to start while an `oriel worker` from an earlier release still answers on ~/.oriel/worker.sock:
// it would run the same sessions. A socket nothing answers on is left from one that's gone, and is deleted.
func oldWorker(home string) error {
	path := filepath.Join(home, "worker.sock")
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		c.Close()
		return fmt.Errorf("an older `oriel worker` is still running: quit it with `pkill -f 'oriel worker'`")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
