package shell

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/trace"
	"strings"
	"sync"
	"time"
)

// stallGap is the least time between traces written, so a slow stretch writes one, not one a frame.
const stallGap = 10 * time.Second

// stallTrace keeps the last few seconds of execution trace, and writes them to a file when the window stalls: the
// first to path, later ones beside it numbered. A nil one does nothing.
type stallTrace struct {
	fr      *trace.FlightRecorder
	path    string
	log     *slog.Logger
	mu      sync.Mutex // n and written: the loop never waits on a write
	n       int
	written time.Time
	wmu     sync.Mutex // a write, and stopping
	stopped bool       // under wmu: writes still to come write nothing
	writes  sync.WaitGroup
}

func startStallTrace(path string, log *slog.Logger) *stallTrace {
	if path == "" {
		return nil
	}
	fr := trace.NewFlightRecorder(trace.FlightRecorderConfig{MinAge: 10 * time.Second, MaxBytes: 64 << 20})
	if err := fr.Start(); err != nil {
		log.Error("no trace of stalls", "err", err)
		return nil
	}
	return &stallTrace{fr: fr, path: path, log: log}
}

// stalled writes the trace, off the caller's goroutine.
func (s *stallTrace) stalled() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if time.Since(s.written) < stallGap {
		s.mu.Unlock()
		return
	}
	s.written = time.Now()
	s.n++
	path := s.path
	if s.n > 1 {
		ext := filepath.Ext(path)
		path = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(path, ext), s.n, ext)
	}
	s.mu.Unlock()
	s.writes.Go(func() {
		s.wmu.Lock()
		defer s.wmu.Unlock()
		if s.stopped {
			return
		}
		f, err := os.Create(path)
		if err == nil {
			_, err = s.fr.WriteTo(f)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			s.log.Error("writing the stall's trace", "path", path, "err", err)
			return
		}
		s.log.Warn("wrote the stall's trace", "path", path)
	})
}

// stop stops recording, once a write under way is done; writes asked for and not yet begun are dropped.
func (s *stallTrace) stop() {
	if s == nil {
		return
	}
	s.wmu.Lock()
	s.stopped = true
	s.fr.Stop()
	s.wmu.Unlock()
	s.writes.Wait()
}
