package logs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Dir is where Oriel writes its log: ~/Library/Logs/Oriel on macOS, $XDG_STATE_HOME/oriel elsewhere.
func Dir() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Logs", "Oriel")
	}
	return filepath.Join(stateHome(home), "oriel")
}

// StateDir is where Oriel keeps state that isn't config: session logs, crash reports.
func StateDir() string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Oriel")
	}
	return filepath.Join(stateHome(home), "oriel")
}

func stateHome(home string) string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d
	}
	return filepath.Join(home, ".local", "state")
}

// File writes records as text lines to a file, moving it to path.1 (and path.1 to path.2, up to Keep) when it
// passes MaxSize.
type File struct {
	Path    string
	MaxSize int64 // 10 MiB if 0
	Keep    int   // 3 if 0

	mu   sync.Mutex
	f    *os.File
	size int64
	err  error
}

func (f *File) Write(r Record) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return
	}
	line := r.Text() + "\n"
	if f.f == nil {
		if f.err = f.open(); f.err != nil {
			return
		}
	}
	if f.size > 0 && f.size+int64(len(line)) > f.maxSize() {
		if f.err = f.rotate(); f.err != nil {
			return
		}
	}
	n, err := f.f.WriteString(line)
	f.size += int64(n)
	f.err = err
}

// Err is the error that stopped writing, if any.
func (f *File) Err() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.f == nil {
		return nil
	}
	err := f.f.Close()
	f.f = nil
	return err
}

func (f *File) maxSize() int64 {
	if f.MaxSize > 0 {
		return f.MaxSize
	}
	return 10 << 20
}

func (f *File) open() error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	fh, err := os.OpenFile(f.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := fh.Stat()
	if err != nil {
		fh.Close()
		return err
	}
	f.f, f.size = fh, st.Size()
	return nil
}

func (f *File) rotate() error {
	f.f.Close()
	f.f = nil
	keep := f.Keep
	if keep <= 0 {
		keep = 3
	}
	for i := keep - 1; i >= 1; i-- {
		os.Rename(fmt.Sprintf("%s.%d", f.Path, i), fmt.Sprintf("%s.%d", f.Path, i+1))
	}
	if err := os.Rename(f.Path, f.Path+".1"); err != nil {
		return err
	}
	return f.open()
}
