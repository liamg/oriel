package settings_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/settings"

	"github.com/liamg/oriel/app/apptest"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/registry"
)

// The Prompts page edits prompts, including Coding style (also written into the generated CLAUDE.md), and stores
// only overrides. Edited on the page, saved with ⌘S, kept as a file of its own, sent to
// the session host; reset, the file goes and the host has the default again.
func TestThePromptsPage(t *testing.T) {
	s, lw := apptest.HostScene(t)
	s.Run("prompts.open", nil)
	s.Frame()
	s.Frame()
	s.WantFrame("PROMPTS", "0 yours", "STYLE", "Coding style", "SESSION PACKS", "Work", "PACK PARTS", "default")
	s.Lint("the Prompts page")

	s.TypeInto("Coding style text", "\nUse tabs.")
	s.WantFrame("not saved")
	s.Press("<D-s>")
	path := filepath.Join(s.ConfigDir, "prompts", "style.code.md")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "\nUse tabs.") {
		t.Fatalf("the style's file: %q %v", b, err)
	}
	s.WantFrame("1 yours", "saved · sessions use it from now on")
	s.Until("the host to have the user's style", func() bool {
		o, sent := lw.W.Prompts()
		return sent && strings.Contains(o[prompts.StyleID], "\nUse tabs.")
	})
	if files, _ := os.ReadDir(filepath.Join(s.ConfigDir, "agents")); len(files) != 0 {
		t.Errorf("prompts left alone were stored: %v", files)
	}

	s.ClickText("Reset to default")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("reset kept the file")
	}
	s.Until("the host to have the default style", func() bool {
		o, _ := lw.W.Prompts()
		return len(o) == 0
	})
}

// A part edited on the page is in the pack a new session's agent starts with.
func TestAPartReachesANewSessionsAgent(t *testing.T) {
	s, lw := apptest.HostScene(t)
	s.Run("prompts.set", registry.Args{"id": prompts.PartID("drafts"), "text": "Leave the drafts to me."})
	s.Until("the host to have the part", func() bool { o, _ := lw.W.Prompts(); return o.Custom(prompts.PartID("drafts")) })
	p, ok := func() (string, bool) {
		o, _ := lw.W.Prompts()
		pk, ok := o.Pack("work", nil)
		return pk.Text, ok
	}()
	if !ok || !strings.Contains(p, "Leave the drafts to me.") {
		t.Errorf("the work pack on the host hasn't the part: %.200q", p)
	}
	if err := s.Registry().Run(context.Background(), "prompts.set", registry.Args{"id": "pack.work", "text": "<!-- include: nosuchpart -->"}); err == nil {
		t.Error("a pack that doesn't build was saved")
	}
}

// A list page's rows hand up the keys they don't use: Escape after ↓ closes the page.
func TestEscapeLeavesAListPage(t *testing.T) {
	s, _ := apptest.HostScene(t)
	s.Run("prompts.open", nil)
	s.Frame()
	s.Frame()
	s.Click(apptest.Centre(s.Within("Prompts list", "Coding style").Bounds), 0)
	s.Frame()
	was := apptest.PluginOf[*settings.Prompts](s, "prompts").Selected()
	s.Type("Down")
	s.Frame()
	if apptest.PluginOf[*settings.Prompts](s, "prompts").Selected() == was {
		t.Fatalf("↓ on the list didn't move from %q", was)
	}
	s.Type("Esc")
	s.Frame()
	if s.Shown("prompts") {
		t.Error("Escape after ↓ left the Prompts page up")
	}
}
