package shell_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry"
)

// rawKeys is a key written in vim's notation, as a person shouldn't have to read it: <C-r>, <D-n>, <CR>.
var rawKeys = regexp.MustCompile(`<(?:[CMADS]-[^>]+|CR|Esc|Space|BS|Tab|Up|Down|Left|Right|Del|Home|End|PageUp|PageDown|Leader|F\d+)>`)

var pageID = regexp.MustCompile(`(?m)^id: *(\S+)`)

// Keys read as a keyboard shows them wherever they're listed: a help page's keys, which-key's next keys, the keymaps
// picker, the omnibox's command preview. ⌘N, ⌃W, ↵, not <D-n>, <C-w>, <CR>.
func TestKeysReadAsAKeyboardShowsThem(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	pages, _ := filepath.Glob("help/pages/*.md")
	more, _ := filepath.Glob("help/pages/*/*.md")
	for _, p := range append(pages, more...) {
		b, _ := os.ReadFile(p)
		m := pageID.FindStringSubmatch(string(b))
		if m == nil || !strings.Contains(string(b), "{{keys") {
			continue
		}
		s.Run("help.open", registry.Args{"id": m[1]})
		s.Frame()
		if raw := rawKeys.FindAllString(s.FullText(), -1); len(raw) > 0 {
			t.Errorf("help page %s lists keys in vim's notation: %v", m[1], raw)
		}
	}
	s.Run("help.open", registry.Args{"id": "shell/start"})
	s.Frame()
	if !strings.Contains(s.FullText(), "⌘N") {
		t.Errorf("shell/start's keys don't show New Session's ⌘N:\n%s", s.FullText())
	}
	s.Run("help.open", registry.Args{"id": "shell/tabs#launch"})
	s.Frame()
	if !strings.Contains(s.FullText(), "⌃O back · ⌃I forward") {
		t.Errorf("the help page's footer doesn't say ⌃O back · ⌃I forward:\n%s", s.FullText())
	}
	s.Golden("help-keys")
	help(s).Close()

	s.Run("picker.open", registry.Args{"source": "keymaps", "query": "New Session"})
	s.Frame()
	s.Frame()
	if text := s.FullText(); !strings.Contains(text, "⌘N") || rawKeys.MatchString(text) {
		t.Errorf("the keymaps picker:\n%s", text)
	}
	s.Press("<Esc>")
	s.Run("omnibox.open", registry.Args{"query": ">settings.open"})
	s.Frame()
	s.Frame()
	if text := s.FullText(); !strings.Contains(text, "global ⌘,") || rawKeys.MatchString(text) {
		t.Errorf("the omnibox's command preview:\n%s", text)
	}
}

// Which-key lists the keys that come next as a keyboard shows them.
func TestWhichKeyShowsGlyphs(t *testing.T) {
	s := apptest.EditorScene(t)
	s.Registry().Command(registry.Command{ID: "test.noop", Run: func(registry.Ctx, registry.Args) error { return nil }})
	s.Registry().Keymap(registry.Keymap{Mode: "n", Keys: "<Space>q<C-r>", Command: "test.noop", Desc: "redo the thing"})
	s.Registry().Keymap(registry.Keymap{Mode: "n", Keys: "<Space>q<CR>", Command: "test.noop", Desc: "enter the thing"})
	keys := registry.NewResolver(s.Registry())
	shell.SetKeys(s.Headless, keys)
	start := s.Now
	keys.Feed([]string{"n", "global"}, " ", start)
	keys.Feed([]string{"n", "global"}, "q", start)
	s.Now = start.Add(shell.WhichKeyDelay)
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "⌃R") || !strings.Contains(text, "↵") || rawKeys.MatchString(text) {
		t.Errorf("which-key's next keys:\n%s", text)
	}
}
