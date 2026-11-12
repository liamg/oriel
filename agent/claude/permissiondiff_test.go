package claude

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/eventq"
	"github.com/liamg/oriel/internal/isolate"
)

// A permission for an edit carries what the edit would do, read from its input as the transcript reads a done one,
// so the prompt can show it before it's allowed.
func TestAnEditsPermissionCarriesItsDiff(t *testing.T) {
	isolate.Test(t)
	q := eventq.New()
	defer q.Close()
	s := &session{events: q, asks: map[string]chan agent.Decision{}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.permission(ctx, permissionIn{ID: "p1", Tool: "Edit", Input: json.RawMessage(`{"file_path":"round.go","old_string":"a\nb","new_string":"a\nc"}`)})
	}()
	p, ok := (<-q.Out()).(agent.Permission)
	cancel()
	<-done
	if !ok || p.Diff == nil {
		t.Fatalf("the permission has no diff: %+v", p)
	}
	if p.Diff.Path != "round.go" || p.Diff.Added != 2 || p.Diff.Removed != 2 || len(p.Diff.Lines) != 4 || p.Diff.Lines[3].Text != "c" {
		t.Errorf("diff = %+v", p.Diff)
	}
}
