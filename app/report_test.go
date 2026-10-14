package app

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
)

// Copy report keeps a stack as lines: pasted into an issue it reads as a trace, not one escaped string.
func TestReportKeepsTracesAsLines(t *testing.T) {
	s := newErrScene(t, "")
	s.host.Notify(core.Notice{ID: "turned-off.view.explorer", Severity: core.SevError, Text: "view explorer is turned off: panic",
		Detail: "goroutine 66 [running]:\nruntime/debug.Stack()\n\t/go/src/runtime/debug/stack.go:26 +0x64"})
	s.host.Log().Error("view explorer is turned off", "detail", "goroutine 66 [running]:\nruntime/debug.Stack()")
	r := s.sh.report("")
	if strings.Contains(r, `\n`) || strings.Contains(r, `\t`) {
		t.Errorf("the report has escaped newlines or tabs:\n%s", r)
	}
	for _, want := range []string{"\n    goroutine 66 [running]:\n", "\n    runtime/debug.Stack()\n"} {
		if !strings.Contains(r, want) {
			t.Errorf("the report lacks %q as its own line:\n%s", want, r)
		}
	}
}
