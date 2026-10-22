package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
)

// Copy report keeps a stack as lines: pasted into an issue it reads as a trace, not one escaped string.
func TestReportKeepsTracesAsLines(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Host().Notify(core.Notice{ID: "turned-off.view.explorer", Severity: core.SevError, Text: "view explorer is turned off: panic",
		Detail: "goroutine 66 [running]:\nruntime/debug.Stack()\n\t/go/src/runtime/debug/stack.go:26 +0x64"})
	s.Host().Log().Error("view explorer is turned off", "detail", "goroutine 66 [running]:\nruntime/debug.Stack()")
	r := shell.Report(s.Headless)
	if strings.Contains(r, `\n`) || strings.Contains(r, `\t`) {
		t.Errorf("the report has escaped newlines or tabs:\n%s", r)
	}
	for _, want := range []string{"\n    goroutine 66 [running]:\n", "\n    runtime/debug.Stack()\n"} {
		if !strings.Contains(r, want) {
			t.Errorf("the report lacks %q as its own line:\n%s", want, r)
		}
	}
}

// Copy report lists events by kind, ID and time: what they hold (PR threads, transcripts, commands, paths) stays out,
// and the home folder is ~.
func TestReportLeavesEventPayloadsOut(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "src", "acme-billing")
	for _, ev := range []core.Event{
		core.SessionSynced{Session: core.SessionState{ID: "s1", Name: "Refund flow", Dir: dir, Prompt: "fix the refund for customer Jane Roe"}},
		core.ThreadUpdated{Session: "s1", Thread: core.Thread{ID: "t1", Status: core.ThreadOpen, Hosted: &core.HostThread{Repo: "acme/billing", PR: 7},
			Comments: []core.Comment{{ID: "c1", Author: core.AuthorHost, Name: "priya-k", Body: "the vat rate for Ireland is wrong"}}}},
		core.TranscriptAppended{Session: "s1", Items: []core.Item{{ID: "i1", Kind: "tool", Tool: "Bash",
			Input: `{"command":"curl -H 'Authorization: Bearer sk-live-4242' https://api.acme.test"}`, Output: "invoice 991 for Jane Roe", Duration: 1200}}},
	} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Host().Log().Info("opened " + filepath.Join(dir, "main.go"))
	r := shell.Report(s.Headless)
	for _, leak := range []string{"Jane Roe", "Refund flow", "vat rate", "priya-k", "acme/billing", "sk-live-4242", "curl", "invoice 991", home} {
		if strings.Contains(r, leak) {
			t.Errorf("the report holds %q:\n%s", leak, r)
		}
	}
	for _, want := range []string{"SessionSynced", "ThreadUpdated", "TranscriptAppended", "s1", "~/src/acme-billing/main.go"} {
		if !strings.Contains(r, want) {
			t.Errorf("the report lacks %q:\n%s", want, r)
		}
	}
}
