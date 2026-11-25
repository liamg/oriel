package agentui_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// A request's tab, opened or switched back to, puts you in its reply field: what's typed is the reply.
func TestARequestsTabFocusesItsReplyField(t *testing.T) {
	r, a, _ := apptest.AgentWindow(t)
	r.Run("request.comment", registry.Args{"body": "Round half to even?"})
	a.WaitSent(t)
	th := threadWith(t, r, "Round half to even?")
	r.Run("request.open", registry.Args{"thread": string(th.ID)})
	r.WaitFocused("Write to the request")

	path := filepath.Join(projectDir(t, r), "notes.go")
	if err := os.WriteFile(path, []byte("package pricing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) }); err != nil {
		t.Fatal(err)
	}
	r.WaitAccess("the file in front", func(r *apptest.Window) bool {
		n := r.Access("Write to the request")
		return n == nil || !n.Focused
	})
	r.ClickText("Round half to even?") // the request's tab
	r.WaitFocused("Write to the request")
	r.TypeText("Yes, banker's")
	r.WaitAccess("the reply typed in its field", func(r *apptest.Window) bool {
		n := r.Access("Write to the request")
		return n != nil && n.Value == "Yes, banker's"
	})
}
