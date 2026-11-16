package diag

import (
	"io"
	"log/slog"
	"time"
)

// What the package's outside tests do to its stall trace.

// StartQuietStallTrace starts the stall trace written to path, logging nowhere, or answers false where there's no
// flight recorder.
func StartQuietStallTrace(path string) (*StallTrace, bool) {
	s := StartStallTrace(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, s != nil
}

func (s *StallTrace) HoldWrite()    { s.wmu.Lock() }
func (s *StallTrace) ReleaseWrite() { s.wmu.Unlock() }
func (s *StallTrace) Forget()       { s.written = time.Time{} }
