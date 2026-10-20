package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
)

// The user's versions of the prompts, once the app sends them, are what a session's agent starts with; they last
// a restart, for sessions that start with no app connected.
func TestTheUsersPromptsReachTheAgent(t *testing.T) {
	r := newRig(t)
	_, a := r.workSession("first")
	if a.Spec.Pack.Text != "pack work" {
		t.Fatalf("before the app sends prompts, the pack is Packs': %q", a.Spec.Pack.Text)
	}
	o := prompts.Overrides{prompts.PartID("drafts"): "Never write drafts.\n", prompts.StyleID: "Tabs."}
	if err := r.w.SetPrompts(o); err != nil {
		t.Fatal(err)
	}
	r.restart()
	_, a = r.workSession("second")
	if !strings.Contains(a.Spec.Pack.Text, "Never write drafts.") || !strings.Contains(a.Spec.Pack.Text, "# Work") {
		t.Errorf("the work pack hasn't the user's drafts part: %.200q", a.Spec.Pack.Text)
	}
	if err := r.w.SetPrompts(prompts.Overrides{}); err != nil {
		t.Fatal(err)
	}
	_, a = r.workSession("third")
	if a.Spec.Pack.Text != "pack work" {
		t.Errorf("reset, the work pack should be Packs' again: %.200q", a.Spec.Pack.Text)
	}
}

// crit-reference.md, "Prompts page": Coding style is "also written into the generated CLAUDE.md". crit's
// WriteAgentNotes lists the projects and where their instructions are, then "## Coding style".
func TestWorkspaceNotesCarryTheCodingStyle(t *testing.T) {
	r := newRig(t)
	r.repo.Write(map[string]string{"CLAUDE.md": "repo rules\n"})
	r.repo.Commit("Ada", "rules")
	if err := r.w.SetPrompts(prompts.Overrides{prompts.StyleID: "Tabs, never spaces.\n"}); err != nil {
		t.Fatal(err)
	}
	id, _ := r.workSession("Work")
	se := r.session(id)
	p, _ := se.Primary()
	rel, _ := filepath.Rel(se.Dir, p.Dir)
	for _, name := range NotesFiles {
		b, err := os.ReadFile(filepath.Join(se.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		notes := string(b)
		for _, want := range []string{"# Workspace: ", "- `" + rel + "` — " + p.Repo + " — see `" + filepath.Join(rel, "CLAUDE.md") + "`",
			"\n## Coding style\n\nTabs, never spaces.\n"} {
			if !strings.Contains(notes, want) {
				t.Errorf("%s hasn't %q:\n%s", name, want, notes)
			}
		}
	}
	out, err := runGit(context.Background(), se.Dir, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "CLAUDE.md") || strings.Contains(out, "AGENTS.md") {
		t.Errorf("the notes show in the workspace's diff: %q", out)
	}

	if err := r.w.SetPrompts(prompts.Overrides{}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(se.Dir, "CLAUDE.md"))
	d, _ := prompts.Lookup(prompts.StyleID)
	if !strings.Contains(string(b), "## Coding style\n\n"+d.Default+"\n") {
		t.Errorf("reset, the notes should have the default style:\n%s", b)
	}
}

// A direct session runs in the user's own directory: Oriel writes nothing there.
func TestNoNotesInTheUsersOwnDirectory(t *testing.T) {
	r := newRig(t)
	if err := r.w.SetPrompts(prompts.Overrides{prompts.StyleID: "Tabs."}); err != nil {
		t.Fatal(err)
	}
	r.start(core.SessionSpec{Kind: core.KindDirect, Dir: r.repo.Dir, Provider: "fake"})
	r.agent()
	for _, name := range NotesFiles {
		if _, err := os.Stat(filepath.Join(r.repo.Dir, name)); err == nil {
			t.Errorf("%s was written in the user's checkout", name)
		}
	}
}
