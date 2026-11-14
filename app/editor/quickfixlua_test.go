package editor_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
)

// quickfix.set takes its list from Lua, where a list is a table and its numbers floats: each item keeps its path,
// line and text.
func TestQuickfixSetFromLua(t *testing.T) {
	s := apptest.EditorScene(t)
	s.Lua(`L.run("quickfix.set", { title = "lint", items = { { path = "main.go", line = 3, col = 2, text = "unused x" }, { path = "util.go", line = 9, text = "shadowed y" } } })`)
	s.Run("quickfix.open", nil)
	s.Frame()
	text := s.Text()
	for _, want := range []string{"unused x", "shadowed y", "main.go", "util.go"} {
		if !strings.Contains(text, want) {
			t.Errorf("the quickfix list lacks %q:\n%s", want, text)
		}
	}
}
