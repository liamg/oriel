package claude

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
)

// The screen reader finds the recorded dialog's footer ("Enter to confirm · Esc to cancel") however the output is
// split, with its question as the title; the footer's words in the agent's prose are no dialog.
func TestScreenDialog(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "external-imports.pty"))
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{len(b), 7, 1} {
		var sc screen
		var got []string
		for i := 0; i < len(b); i += size {
			if out, ok := sc.read(b[i:min(i+size, len(b))]); ok && out.dialog {
				got = append(got, out.title)
			}
		}
		if len(got) != 1 || got[0] != "Allow external CLAUDE.md file imports?" {
			t.Errorf("in chunks of %d: %q", size, got)
		}
	}
	var sc screen
	if out, ok := sc.read([]byte("⏺ Its dialogs end Enter to confirm · Esc to cancel\r\n")); ok {
		t.Errorf("prose read as %+v", out)
	}
}

// A dialog on claude's terminal as it starts raises Dialog, once while it stays, and Enter typed there answers it.
func TestDialogFromTheScreen(t *testing.T) {
	t.Setenv("ORIEL_FAKE_IMPORTS", "1")
	s := agenttest.Start(t, fakeHarness(t))
	ev := s.Wait("Dialog", func(ev agent.Event) bool { _, ok := ev.(agent.Dialog); return ok }).(agent.Dialog)
	if ev != (agent.Dialog{Title: "Allow external CLAUDE.md file imports?", Shown: true}) {
		t.Errorf("Dialog = %+v", ev)
	}
	if _, err := s.S.Terminal().Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	s.Wait("Dialog gone", func(ev agent.Event) bool { d, ok := ev.(agent.Dialog); return ok && !d.Shown })
	n := 0
	for _, ev := range s.Events {
		if d, ok := ev.(agent.Dialog); ok && d.Shown {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Dialog raised %d times for one screen", n)
	}
}

// Enter and Esc answer a dialog; arrow keys (CSI and SS3 forms) and text don't.
func TestAnswersDialog(t *testing.T) {
	for in, want := range map[string]bool{"\r": true, "\x1b": true, "x\x1b": true, "\x1b[B": false, "\x1bOA": false, "jk": false} {
		if got := answersDialog([]byte(in)); got != want {
			t.Errorf("answersDialog(%q) = %v, want %v", in, got, want)
		}
	}
}
