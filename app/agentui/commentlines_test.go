package agentui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
)

// A comment takes several lines: ⇧⏎ starts a new line and ⏎ sends it, in a new thread on a line, a thread's reply and
// the Requests composer; what's sent keeps its lines, and a blank line between them shows as two paragraphs.
func TestCommentsTakeLines(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.WaitText("floor := scaled.Floor()")
	r.TypeKeys("4G")
	r.WaitFrame("the cursor on line 4", func(s string) bool { return strings.Contains(s, "\n4:") })
	r.Do(func() error { return r.Host.Run(context.Background(), "thread.new", nil) })
	r.WaitText("New thread")
	r.TypeText("Floor goes the wrong way.")
	r.TypeKeys("<S-CR><S-CR>")
	r.TypeText("And for zero?")
	r.WaitFrame("both lines in the field", func(s string) bool { return apptest.HasAll(s, "Floor goes the wrong way.", "And for zero?") })
	a.NoneSent(t)
	r.TypeKeys("<CR>")
	body := "Floor goes the wrong way.\n\nAnd for zero?"
	if in := a.WaitSent(t); !strings.Contains(in.Text, body) {
		t.Errorf("the new thread sent %q, want %q in it", in.Text, body)
	}
	th := threadWith(t, r, body)
	r.WaitFrame("the thread's comment as two paragraphs", func(s string) bool {
		return apptest.HasAll(s, "\nFloor goes the wrong way.\n", "\nAnd for zero?\n")
	})

	rig.Answered(t, r.Store, a)
	a.Emit(agent.TurnEnd{})
	r.ClickText("Reply…")
	r.TypeText("cover -2.5")
	r.TypeKeys("<S-CR>")
	r.TypeText("and -0.5")
	r.WaitText("and -0.5")
	r.TypeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "cover -2.5\nand -0.5") {
		t.Errorf("the inline reply sent %q", in.Text)
	}

	rig.Answered(t, r.Store, a)
	a.Emit(agent.TurnEnd{})
	r.TypeKeys("<D-R>")
	r.WaitText("New request")
	r.Do(func() error {
		return r.Host.Run(context.Background(), "request.open", registry.Args{"thread": string(th.ID)})
	})
	r.ClickText("Reply, or ask more")
	r.TypeText("one")
	r.TypeKeys("<S-CR>")
	r.TypeText("two")
	r.WaitText("two")
	r.TypeKeys("<CR>")
	if in := a.WaitSent(t); !strings.Contains(in.Text, "one\ntwo") {
		t.Errorf("the Requests composer sent %q", in.Text)
	}
}
