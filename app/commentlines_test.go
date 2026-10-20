package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/registry"
)

// A comment takes several lines: ⇧⏎ starts a new line and ⏎ sends it, in a new thread on a line, a thread's reply and
// the Requests composer; what's sent keeps its lines, and a blank line between them shows as two paragraphs.
func TestCommentsTakeLines(t *testing.T) {
	r, a, _ := agentSession(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.do(func() error { return r.host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.waitText("floor := scaled.Floor()")
	r.typeKeys("4G")
	r.waitFrame("the cursor on line 4", func(s string) bool { return strings.Contains(s, "\n4:") })
	r.do(func() error { return r.host.Run(context.Background(), "thread.new", nil) })
	r.waitText("New thread")
	r.typeText("Floor goes the wrong way.")
	r.typeKeys("<S-CR><S-CR>")
	r.typeText("And for zero?")
	r.waitFrame("both lines in the field", func(s string) bool { return containsAll(s, "Floor goes the wrong way.", "And for zero?") })
	a.NoneSent(t)
	r.typeKeys("<CR>")
	body := "Floor goes the wrong way.\n\nAnd for zero?"
	if in := a.WaitSent(t); !strings.Contains(in.Text, body) {
		t.Errorf("the new thread sent %q, want %q in it", in.Text, body)
	}
	th := threadWith(t, r, body)
	r.waitFrame("the thread's comment as two paragraphs", func(s string) bool {
		return containsAll(s, "\nFloor goes the wrong way.\n", "\nAnd for zero?\n")
	})

	a.Emit(agent.TurnEnd{})
	r.clickText("Reply…")
	r.typeText("cover -2.5")
	r.typeKeys("<S-CR>")
	r.typeText("and -0.5")
	r.waitText("and -0.5")
	r.typeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "cover -2.5\nand -0.5") {
		t.Errorf("the inline reply sent %q", in.Text)
	}

	a.Emit(agent.TurnEnd{})
	r.typeKeys("<D-R>")
	r.waitText("New request")
	r.do(func() error {
		return r.host.Run(context.Background(), "request.open", registry.Args{"thread": string(th.ID)})
	})
	r.clickText("Reply, or ask more")
	r.typeText("one")
	r.typeKeys("<S-CR>")
	r.typeText("two")
	r.waitText("two")
	r.typeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "one\ntwo") {
		t.Errorf("the Requests composer sent %q", in.Text)
	}
}
