package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/registry"
)

// crit-reference.md, "Prompts page": "editable prompts, including Coding style (also written into the generated
// CLAUDE.md). Only overrides are stored." Edited on the page, saved with ⌘S, kept as a file of its own, sent to
// the session host; reset, the file goes and the host has the default again.
func TestThePromptsPage(t *testing.T) {
	s, lw := hostScene(t)
	s.run("prompts.open", nil)
	s.frame()
	s.frame()
	s.wantFrame(t, "PROMPTS", "0 yours", "STYLE", "Coding style", "SESSION PACKS", "Work", "PACK PARTS", "default")
	s.lint("the Prompts page")

	s.typeInto(t, "Coding style text", "\nUse tabs.")
	s.wantFrame(t, "not saved")
	s.keyCommand(t, "<D-s>")
	path := filepath.Join(s.sh.cfg.ConfigDir, "prompts", "style.code.md")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "\nUse tabs.") {
		t.Fatalf("the style's file: %q %v", b, err)
	}
	s.wantFrame(t, "1 yours", "saved · sessions use it from now on")
	s.until(t, "the host to have the user's style", func() bool {
		o, sent := lw.w.Prompts()
		return sent && strings.Contains(o[prompts.StyleID], "\nUse tabs.")
	})
	if files, _ := os.ReadDir(filepath.Join(s.sh.cfg.ConfigDir, "agents")); len(files) != 0 {
		t.Errorf("prompts left alone were stored: %v", files)
	}

	s.clickText(t, "Reset to default")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("reset kept the file")
	}
	s.until(t, "the host to have the default style", func() bool {
		o, _ := lw.w.Prompts()
		return len(o) == 0
	})
}

// A part edited on the page is in the pack a new session's agent starts with.
func TestAPartReachesANewSessionsAgent(t *testing.T) {
	s, lw := hostScene(t)
	s.run("prompts.set", registry.Args{"id": prompts.PartID("drafts"), "text": "Leave the drafts to me."})
	s.until(t, "the host to have the part", func() bool { o, _ := lw.w.Prompts(); return o.Custom(prompts.PartID("drafts")) })
	p, ok := func() (string, bool) {
		o, _ := lw.w.Prompts()
		pk, ok := o.Pack("work", nil)
		return pk.Text, ok
	}()
	if !ok || !strings.Contains(p, "Leave the drafts to me.") {
		t.Errorf("the work pack on the host hasn't the part: %.200q", p)
	}
	if err := s.reg.Run(context.Background(), "prompts.set", registry.Args{"id": "pack.work", "text": "<!-- include: nosuchpart -->"}); err == nil {
		t.Error("a pack that doesn't build was saved")
	}
}

// A list page's rows hand up the keys they don't use: Escape after ↓ closes the page.
func TestEscapeLeavesAListPage(t *testing.T) {
	s, _ := hostScene(t)
	s.run("prompts.open", nil)
	s.frame()
	s.frame()
	s.click(centre(s.within("Prompts list", "Coding style").Bounds))
	s.frame()
	was := s.promptsPage().sel
	s.typeKeys("Down")
	s.frame()
	if s.promptsPage().sel == was {
		t.Fatalf("↓ on the list didn't move from %q", was)
	}
	s.typeKeys("Esc")
	s.frame()
	if s.promptsPage().open {
		t.Error("Escape after ↓ left the Prompts page up")
	}
}
