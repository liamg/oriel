package agentui_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
)

// + under the composer attaches a file on this machine, picked by its path: a folder opens as it's picked, and the
// file goes in as a link to a copy the agent can read. In the conversation the file is a card: ↓ saves a copy where
// it's typed (from ~/Downloads), and a click opens it in a tab.
func TestComposerAttachesAHostFile(t *testing.T) {
	r, a, _ := requestsSession(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"logs", "Downloads"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(home, "logs", "build.log")
	if err := os.WriteFile(log, []byte("boom\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.ClickText("New request")
	r.WaitText("Write a request")
	r.ClickText("Write a request")
	r.TypeText("why did this fail? ")
	r.ClickAccess("Attach a file")
	r.WaitText("~/logs/")
	r.ClickText("~/logs/")
	r.WaitText("~/logs/build.log")
	r.ClickText("~/logs/build.log")
	r.WaitFrame("the link in the composer", func(s string) bool { return strings.Contains(strings.ReplaceAll(s, "\n", ""), "[build.log](") })
	r.WaitFocused("Write to the request")
	r.TypeKeys("<CR>")

	in := a.WaitSent(t)
	var body string
	r.UntilState("the request", func(st core.State) bool {
		se, _ := st.Session(r.Session)
		for _, x := range se.Snapshot().Threads {
			if len(x.Comments) > 0 && strings.HasPrefix(x.Comments[0].Body, "why did this fail?") {
				body = x.Comments[0].Body
				return true
			}
		}
		return false
	})
	m := regexp.MustCompile(`\[build\.log\]\(([^)]+)\)`).FindStringSubmatch(body)
	if m == nil || m[1] == log || !strings.Contains(m[1], filepath.Join("agent", "attachments")) {
		t.Fatalf("not linked to a copy in the agent's folder: %q", body)
	}
	if b, _ := os.ReadFile(m[1]); string(b) != "boom\n" {
		t.Errorf("the copy holds %q", b)
	}
	if !strings.Contains(in.Text, m[1]) {
		t.Errorf("the agent wasn't told where it is:\n%s", in.Text)
	}

	r.WaitAccess("the file's card", func(w *apptest.Window) bool { return w.Access("Open build.log") != nil })
	r.ClickAccess("Save build.log as…")
	r.WaitText("Save as ~/Downloads/build.log")
	r.ClickText("Save as ~/Downloads/build.log")
	r.WaitText("Saved build.log to ~/Downloads/build.log")
	if b, _ := os.ReadFile(filepath.Join(home, "Downloads", "build.log")); string(b) != "boom\n" {
		t.Errorf("the saved copy holds %q", b)
	}

	r.ClickAccess("Open build.log")
	r.WaitText("boom") // its text, in an editor tab
}
