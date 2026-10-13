package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
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

func whichKeyApp(t *testing.T) *running {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha beta\n"), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	g := &grabHost{keys: agentKeys, setup: func(h ext.Host) {
		h.Command(registry.Command{ID: "test.noop", Run: func(registry.Ctx, registry.Args) error { return nil }})
		h.Options().Set("keymap.timeout", registry.LayerUser, registry.At{}, false)
	}}
	r := &running{t: t, store: eventstore.New(), dir: dir, done: make(chan error, 1)}
	go func() {
		r.done <- Run(ctx, be, Config{Store: r.store, ConfigDir: t.TempDir(), WorkDir: dir, Files: []string{"a.txt"}, Extensions: []ext.Extension{g}})
	}()
	r.win = <-be.Windows()
	r.host = g.h
	t.Cleanup(func() { r.win.Close(); <-r.done })
	r.waitText("alpha")
	return r
}

// SH-6: "appears after 300 ms for any prefix, lists desc, ⌫ backs up".
func TestWhichKeyLeader(t *testing.T) {
	r := whichKeyApp(t)
	sent := time.Now()
	r.typeKeys("<Space>")
	r.waitText("shown after 300ms")
	if d := time.Since(sent); d < WhichKeyDelay {
		t.Errorf("which-key showed after %v", d)
	}
	text := r.waitText("+agent")
	for _, want := range []string{"Find files", "Grep", "Keymaps"} {
		if !strings.Contains(text, want) {
			t.Errorf("the leader's keys lack %q:\n%s", want, text)
		}
	}
	r.typeKeys("a")
	text = r.waitText("Explain this change")
	for _, want := range []string{"Interrupt agent", "Staging logs tool", "init.lua:12", "+permissions"} {
		if !strings.Contains(text, want) {
			t.Errorf("+agent lacks %q:\n%s", want, text)
		}
	}
	r.typeKeys("<BS>")
	r.waitFrame("back at the leader", func(s string) bool {
		return strings.Contains(s, "Find files") && !strings.Contains(s, "Interrupt agent")
	})
	// ? lists every keymap under the prefix, in the keymaps picker.
	r.typeKeys("?")
	r.waitFrame("the keymaps picker on <Space>", func(s string) bool {
		return strings.Contains(s, "Keymaps") && strings.Contains(s, "<Space>") && !strings.Contains(s, "shown after")
	})
	r.typeKeys("<Esc>")
	r.typeKeys("<Space>")
	r.waitText("shown after 300ms")
	r.typeKeys("<Esc>")
	r.waitFrame("which-key gone", func(s string) bool { return !strings.Contains(s, "shown after") })
	// Esc cancelled the leader: the space didn't move the cursor.
	r.typeKeys("x:w<CR>")
	r.waitFile("a.txt", "lpha beta\n")
}

// Any prefix: vim's own g lists the engine's commands.
func TestWhichKeyEnginePrefix(t *testing.T) {
	r := whichKeyApp(t)
	r.typeKeys("g")
	text := r.waitText("First line")
	if !strings.Contains(text, "Reselect the last Visual area") {
		t.Errorf("g's keys:\n%s", text)
	}
	r.typeKeys("g")
	r.waitFrame("which-key gone", func(s string) bool { return !strings.Contains(s, "First line") })
}

// 27-whichkey: ␣a's +agent group, against a golden.
func TestWhichKeySnapshot(t *testing.T) {
	s := editorScene(t)
	s.reg.Command(registry.Command{ID: "test.noop", Run: func(registry.Ctx, registry.Args) error { return nil }})
	for _, k := range agentKeys {
		s.reg.Keymap(k)
	}
	keys := registry.NewResolver(s.reg)
	s.sh.keys = keys
	start := s.now
	keys.Feed([]string{"n", "global"}, " ", start)
	keys.Feed([]string{"n", "global"}, "a", start)
	s.now = start.Add(WhichKeyDelay)
	s.golden("whichkey")
	if !strings.Contains(s.frameText(), "+agent") {
		t.Errorf("no +agent:\n%s", s.frameText())
	}
}
