package editor_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/registry"
)

// repeat.txt: "q{0-9a-zA-Z"}	Record typed characters into register" and "q	Stops recording." While q records, the
// status line says which register, beside the mode, and the chip goes when q stops.
func TestRecordingChip(t *testing.T) {
	s, root := searchScene(t)
	s.Run("editor.open", registry.Args{"path": root + "/internal/pricing/round.go"})
	waitHighlights(s)
	keys(s, "qq")
	s.Frame()
	s.WantFrame("REC @q")
	if mode, n := s.Node("Mode"), s.Node("Recording"); mode.Bounds.Min.Y != n.Bounds.Min.Y || n.Bounds.Min.X < mode.Bounds.Max.X {
		t.Errorf("the recording chip at %v isn't beside the mode at %v", n.Bounds, mode.Bounds)
	}
	keys(s, "q")
	s.Frame()
	if strings.Contains(s.Text(), "REC @") {
		t.Error("the recording chip stayed after q")
	}
}
