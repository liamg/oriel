package agentui_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

// A thread between a file's lines opens in full view in Requests, and the request's Open in editor goes back to the
// file with the thread in view.
func TestAThreadBetweenTheLinesOpensInFullViewAndBack(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	path := filepath.Join(projectDir(t, r), "long.go")
	var src strings.Builder
	src.WriteString("package pricing\n\n")
	for i := range 200 {
		fmt.Fprintf(&src, "var v%d = %d\n", i, i)
	}
	if err := os.WriteFile(path, []byte(src.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	open := func(line int) {
		t.Helper()
		if err := r.Do(func() error {
			return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path, "line": line})
		}); err != nil {
			t.Fatal(err)
		}
	}
	open(150)
	r.WaitText("var v147 = 147")
	r.Run("request.comment", registry.Args{"file": path, "line": 150, "body": "Why one hundred and forty-seven?"})
	a.WaitSent(t)
	card := "Thread Why one hundred and forty-seven?"
	r.WaitAccess("the card's full view link", func(r *apptest.Window) bool { return inThread(r, card, "Full view") })
	r.ClickAccessIn(card, "Full view")
	r.WaitAccess("the request in full view", func(r *apptest.Window) bool { return r.Access("Open in editor ↗") != nil })
	r.Latest("the request in front of the file", func(f headless.Frame) bool { return strings.Contains(f.Text(), "THIS REQUEST") })
	r.ClickAccess("Open in editor ↗")
	r.Latest("the editor back at the thread", func(f headless.Frame) bool {
		text := f.Text()
		return !strings.Contains(text, "THIS REQUEST") && strings.Contains(text, "v147") && strings.Contains(text, "Why one hundred and forty-seven?")
	})
}
