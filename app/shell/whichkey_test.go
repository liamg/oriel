package shell_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// agentKeys are 27-whichkey's +agent group.
var agentKeys = []registry.Keymap{
	{Mode: "n", Keys: "<Leader>a", Desc: "+agent"},
	{Mode: "n", Keys: "<Leader>ae", Command: "test.noop", Desc: "Explain this change"},
	{Mode: "n", Keys: "<Leader>ai", Command: "test.noop", Desc: "Interrupt agent"},
	{Mode: "n", Keys: "<Leader>al", Command: "test.noop", Desc: "Staging logs tool", Source: "init.lua:12"},
	{Mode: "n", Keys: "<Leader>ap", Desc: "+permissions"},
	{Mode: "n", Keys: "<Leader>apa", Command: "test.noop", Desc: "Allow"},
	{Mode: "n", Keys: "<Leader>apd", Command: "test.noop", Desc: "Deny"},
}

func whichKeyApp(t *testing.T) *apptest.Window {
	t.Helper()
	return whichKeyAppWith(t, func(h plugin.Host) { h.Options().Set("keymap.timeout", registry.LayerUser, registry.At{}, false) })
}

func whichKeyAppWith(t *testing.T, opts func(h plugin.Host)) *apptest.Window {
	t.Helper()
	r := apptest.NewWindow(t, apptest.WindowOptions{Write: map[string]string{"a.txt": "alpha beta\n"}, Files: []string{"a.txt"}, Keys: agentKeys,
		Setup: func(h plugin.Host) {
			h.Command(registry.Command{ID: "test.noop", Run: func(registry.Ctx, registry.Args) error { return nil }})
			opts(h)
		}})
	r.WaitText("alpha")
	return r
}

// It appears after 300 ms for any prefix, lists desc, and ⌫ backs up.
func TestWhichKeyLeader(t *testing.T) {
	r := whichKeyApp(t)
	sent := time.Now()
	r.TypeKeys("<Space>")
	r.WaitText("shown after 300ms")
	if d := time.Since(sent); d < shell.WhichKeyDelay {
		t.Errorf("which-key showed after %v", d)
	}
	text := r.WaitText("+agent")
	for _, want := range []string{"Find files", "Grep", "Keymaps"} {
		if !strings.Contains(text, want) {
			t.Errorf("the leader's keys lack %q:\n%s", want, text)
		}
	}
	r.TypeKeys("a")
	text = r.WaitText("Explain this change")
	for _, want := range []string{"Interrupt agent", "Staging logs tool", "init.lua:12", "+permissions"} {
		if !strings.Contains(text, want) {
			t.Errorf("+agent lacks %q:\n%s", want, text)
		}
	}
	r.TypeKeys("<BS>")
	r.WaitFrame("back at the leader", func(s string) bool {
		return strings.Contains(s, "Find files") && !strings.Contains(s, "Interrupt agent")
	})
	// ? lists every keymap under the prefix, in the keymaps picker.
	r.TypeKeys("?")
	r.WaitFrame("the keymaps picker on <Space>", func(s string) bool {
		return strings.Contains(s, "❯\n<Space>\n") && strings.Contains(s, "⌨") && !strings.Contains(s, "shown after")
	})
	r.TypeKeys("<Esc>")
	r.TypeKeys("<Space>")
	r.WaitText("shown after 300ms")
	r.TypeKeys("<Esc>")
	r.WaitFrame("which-key gone", func(s string) bool { return !strings.Contains(s, "shown after") })
	// Esc cancelled the leader: the space didn't move the cursor.
	r.TypeKeys("x:w<CR>")
	r.WaitFile("a.txt", "lpha beta\n")
}

// Any prefix: vim's own g lists the engine's commands.
func TestWhichKeyEnginePrefix(t *testing.T) {
	r := whichKeyApp(t)
	r.TypeKeys("g")
	text := r.WaitText("First line")
	if !strings.Contains(text, "Reselect the last Visual area") {
		t.Errorf("g's keys:\n%s", text)
	}
	r.TypeKeys("g")
	r.WaitFrame("which-key gone", func(s string) bool { return !strings.Contains(s, "First line") })
}

// 27-whichkey: ␣a's +agent group, against a golden.
func TestWhichKeySnapshot(t *testing.T) {
	s := apptest.EditorScene(t)
	s.Registry().Command(registry.Command{ID: "test.noop", Run: func(registry.Ctx, registry.Args) error { return nil }})
	for _, k := range agentKeys {
		s.Registry().Keymap(k)
	}
	keys := registry.NewResolver(s.Registry())
	shell.SetKeys(s.Headless, keys)
	start := s.Now
	keys.Feed([]string{"n", "global"}, " ", start)
	keys.Feed([]string{"n", "global"}, "a", start)
	s.Now = start.Add(shell.WhichKeyDelay)
	s.Golden("whichkey")
	if !strings.Contains(s.Text(), "+agent") {
		t.Errorf("no +agent:\n%s", s.Text())
	}
}

// Once which-key shows a prefix, it waits for the next key past keymap.timeoutlen, as which-key.nvim does.
func TestWhichKeyHoldsThePrefix(t *testing.T) {
	r := whichKeyAppWith(t, func(h plugin.Host) {
		h.Options().Set("keymap.timeoutlen", registry.LayerUser, registry.At{}, 500*time.Millisecond)
	})
	r.TypeKeys("<Space>")
	r.WaitText("shown after 300ms")
	time.Sleep(time.Second)
	if fs := r.Win.Frames(); !strings.Contains(fs[len(fs)-1].Text(), "shown after 300ms") {
		t.Fatalf("which-key went after keymap.timeoutlen:\n%s", fs[len(fs)-1].Text())
	}
	r.TypeKeys("a")
	r.WaitText("Interrupt agent")
}
