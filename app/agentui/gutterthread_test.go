package agentui_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

// The pointer on a line of a file shows a + in its gutter; clicking it opens a thread on that line, between the lines,
// and what's written there reaches the agent anchored to it. Off the line, there's no +.
func TestAPlusInTheGutterOpensAThreadOnTheLine(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	dir := projectDir(t, r)
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte("package pricing\n\nfunc roundHalfEven() {\n\tfloor := scaled.Floor()\n}\n"), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) })
	r.WaitText("floor := scaled.Floor()")
	if r.Access("Comment on line 4") != nil {
		t.Fatal("line 4 shows its + with the pointer nowhere near it")
	}
	r.Hover("scaled", "scaled")
	r.WaitAccess("line 4's + with the pointer on it", func(r *apptest.Window) bool { return r.Access("Comment on line 4") != nil })
	if r.Access("Comment on line 3") != nil {
		t.Error("line 3 shows its + while the pointer is on line 4")
	}
	r.ClickAccess("Comment on line 4")
	r.WaitText("New thread")
	r.TypeText("Floor goes the wrong way.")
	r.WaitText("Floor goes the wrong way.")
	r.TypeKeys("<CR>")
	in := a.WaitSent(t)
	if !strings.Contains(in.Text, "round.go:4") || !strings.Contains(in.Text, "Floor goes the wrong way.") {
		t.Errorf("the agent got %q", in.Text)
	}
	th := threadWith(t, r, "Floor goes the wrong way.")
	if th.Anchor == nil || th.Anchor.Line != 4 {
		t.Errorf("the thread is on %+v, want line 4", th.Anchor)
	}

	// Line 4 has its thread's marker in the gutter now, which opens the thread. The pointer on the line still shows
	// the + (over the line's number), and the marker stays: either can be clicked, and the + opens another thread.
	r.Hover("package", "package")
	r.Hover("scaled", "scaled")
	r.WaitAccess("line 4's + beside its thread's marker", func(r *apptest.Window) bool {
		return r.Access("Comment on line 4") != nil && r.Access(th.Heading()) != nil
	})
	r.ClickAccess("Comment on line 4")
	r.Latest("a second thread being opened on line 4", func(f headless.Frame) bool { return strings.Contains(f.Text(), "New thread") })
}
