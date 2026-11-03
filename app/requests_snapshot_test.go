package app_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

// Mockup 01-ide: the thread open between round.go's lines 46 and 47 with Claude's live state, and the summary
// at the right: requests, the tasks rail, the session's stats; the plan's usage windows in the title bar. Its golden
// is the README's screenshot.
func TestIDEThreadSnapshot(t *testing.T) {
	s := apptest.RequestsScene(t)
	s.Run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.Frame()
	s.Frame()
	s.EditorKeys("46Gzt38zt46G$")
	s.WaitHighlights()
	s.Run("requests.summary", nil)
	s.Frame()
	s.Lint("the editor with a thread and the summary")
	s.Golden("ide-thread")
	text := s.Text()
	for _, want := range []string{"Thread", "· L46 ·", "CLAUDE · WORKING", "t:7d45", "REQUESTS", "1 NEEDS YOU", "1 WORKING", "2 resolved", "TASKS", "2/5",
		"Negative tie handling", "SESSION", "CTX", "44k", "CACHE", "TESTS", "1 fail", "5H", "38%", "WK", "71%"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q", want)
		}
	}
}
