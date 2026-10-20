package host

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
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

// A workspace's agent keeps its memory in its repository's Claude Code memory directory, the one `claude` in the
// checkout uses. With more than one project, the notes say where each project's memories go and load the others'.
func TestWorkspaceMemoryIsTheRepositorys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	r := newRig(t)
	id, a := r.workSession("Work")
	if want := sandbox.MemoryDir(r.repo.Dir); a.Spec.Memory != want {
		t.Errorf("memory = %q, want %q", a.Spec.Memory, want)
	}
	se := r.session(id)
	b, _ := os.ReadFile(filepath.Join(se.Dir, "CLAUDE.md"))
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
	b, _ = os.ReadFile(filepath.Join(se.Dir, "CLAUDE.md"))
	notes := string(b)
	for _, p := range se.Projects {
		if want := "- A memory about " + p.Name + " goes in `" + sandbox.MemoryDir(p.Repo) + "`."; !strings.Contains(notes, want) {
			t.Errorf("notes haven't %q:\n%s", want, notes)
		}
	}
	if want := "\n@" + filepath.Join(sandbox.MemoryDir(other.Dir), "MEMORY.md") + "\n"; !strings.Contains(notes, want) {
		t.Errorf("notes don't import the second project's index %q:\n%s", want, notes)
	}
	if strings.Contains(notes, "@"+filepath.Join(sandbox.MemoryDir(r.repo.Dir), "MEMORY.md")) {
		t.Errorf("notes import the primary's own index:\n%s", notes)
	}
}

// The sandbox's manifest carries the memory directories, the primary's first, whichever order the projects are in.
func TestManifestCarriesTheMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	se := core.SessionState{Kind: core.KindWork, Dir: root, Projects: []core.Project{
		{Name: "b", Repo: "/src/b", Dir: filepath.Join(root, "repos", "b")},
		{Name: "a", Repo: "/src/a", Dir: filepath.Join(root, "repos", "a"), Primary: true},
	}}
	m := manifestOf(root, se).Memory
	want := []sandbox.ProjectMemory{{Name: "a", Dir: sandbox.MemoryDir("/src/a")}, {Name: "b", Dir: sandbox.MemoryDir("/src/b")}}
	if !slices.Equal(m, want) {
		t.Errorf("memory = %+v, want %+v", m, want)
	}
}
