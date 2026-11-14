package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/sandbox"
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

// The Prompts page's Coding style is also written into the generated notes: AGENTS.md, and the file each provider
// reads its own from (CLAUDE.md here). crit's WriteAgentNotes lists the projects and where their instructions are,
// then "## Coding style".
func TestWorkspaceNotesCarryTheCodingStyle(t *testing.T) {
	r := newRig(t)
	r.fake.Has = &agent.Features{Terminal: true, Fork: true, Notes: "CLAUDE.md"}
	r.repo.Write(map[string]string{"CLAUDE.md": "repo rules\n"})
	r.repo.Commit("Ada", "rules")
	if err := r.w.SetPrompts(prompts.Overrides{prompts.StyleID: "Tabs, never spaces.\n"}); err != nil {
		t.Fatal(err)
	}
	id, _ := r.workSession("Work")
	se := r.session(id)
	p, _ := se.Primary()
	rel, _ := filepath.Rel(se.Dir, p.Dir)
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
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
	out, err := gitx.Repo{Dir: se.Dir}.Run(context.Background(), "status", "--porcelain")
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
	for _, name := range r.w.notesFiles() {
		if _, err := os.Stat(filepath.Join(r.repo.Dir, name)); err == nil {
			t.Errorf("%s was written in the user's checkout", name)
		}
	}
}

// A workspace's agent keeps its memory in its repository's Claude Code memory directory, the one `claude` in the
// checkout uses. With more than one project, the notes say where each project's memories go and to read the
// others' indexes, without @-importing them: Claude Code asks about an import from outside the folder on its
// terminal, where nobody is to answer.
func TestWorkspaceMemoryIsTheRepositorys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := newRig(t)
	id, a := r.workSession("Work")
	if want := sandbox.MemoryDir(r.repo.Dir); a.Spec.Memory != want {
		t.Errorf("memory = %q, want %q", a.Spec.Memory, want)
	}
	se := r.session(id)
	b, _ := os.ReadFile(filepath.Join(se.Dir, "AGENTS.md"))
	if strings.Contains(string(b), "## Memory") {
		t.Errorf("one project's notes have memory lines:\n%s", b)
	}

	other := gittest.New(t)
	other.Write(map[string]string{"a": "a\n"})
	other.Commit("Ada", "a")
	if err := r.w.AddRepo(context.Background(), id, core.RepoID(other.Dir)); err != nil {
		t.Fatal(err)
	}
	se = r.session(id)
	b, _ = os.ReadFile(filepath.Join(se.Dir, "AGENTS.md"))
	notes := string(b)
	if !strings.Contains(notes, "Read each of these memory indexes before you start") {
		t.Errorf("notes don't say to read the indexes:\n%s", notes)
	}
	for _, p := range se.Projects {
		if want := "- A memory about " + p.Name + " goes in `" + sandbox.MemoryDir(p.Repo) + "`."; !strings.Contains(notes, want) {
			t.Errorf("notes haven't %q:\n%s", want, notes)
		}
	}
	if want := "\n- `" + filepath.Join(sandbox.MemoryDir(other.Dir), "MEMORY.md") + "`\n"; !strings.Contains(notes, want) {
		t.Errorf("notes don't name the second project's index %q:\n%s", want, notes)
	}
	if strings.Contains(notes, "@/") || strings.Contains(notes, filepath.Join(sandbox.MemoryDir(r.repo.Dir), "MEMORY.md")) {
		t.Errorf("notes import an index, or name the primary's own:\n%s", notes)
	}
}
