package apptest

import (
	"fmt"
	"time"

	"github.com/liamg/oriel/app/explain"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
)

// SettleDrawn waits for the work off the loop that adds to what a frame shows, the window's reads and the gutter's
// explain marks, then draws twice: a snapshot is of the frame as it settles however loaded the machine. What the
// test has queued itself, a move for one, it leaves to the test.
func (s *Scene) SettleDrawn() {
	s.T.Helper()
	var e *explain.Plugin
	if p, ok := s.Plugin("explain"); ok {
		e, _ = p.(*explain.Plugin)
	}
	// A frame takes on what came back, and may start more: a base read in lets a buffer's units be worked out again.
	for deadline := time.Now().Add(10 * time.Second); ; {
		s.SettleReads()
		if e != nil {
			e.Wait()
		}
		s.Frame()
		rd, ok := kit.ReadsOf(s.Registry())
		if (!ok || !rd.Busy()) && (e == nil || !e.Busy()) {
			break
		}
		if time.Now().After(deadline) {
			s.T.Fatalf("what the frame shows never settled: reads busy %v, explain busy %v", ok && rd.Busy(), e != nil && e.Busy())
		}
	}
	s.Frame()
	s.Frame()
}

// drawn waits for a frame with a run of text reading text, as someone waits for what they mean to click to show.
func (s *Scene) drawn(text string) {
	s.T.Helper()
	s.Until(fmt.Sprintf("%q drawn", text), func() bool {
		if s.List() == nil {
			return false
		}
		for _, op := range s.List().Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == text {
				return true
			}
		}
		return false
	})
}
