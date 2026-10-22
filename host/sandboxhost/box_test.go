package sandboxhost

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/sandbox"
)

// The sandbox's manifest carries the memory directories, the primary's first, whichever order the projects are in.
func TestManifestCarriesTheMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	se := core.SessionState{Kind: core.KindWork, Dir: root, Projects: []core.Project{
		{Name: "b", Repo: "/src/b", Dir: filepath.Join(root, "repos", "b")},
		{Name: "a", Repo: "/src/a", Dir: filepath.Join(root, "repos", "a"), Primary: true},
	}}
	m := ManifestOf(root, se).Memory
	want := []sandbox.ProjectMemory{{Name: "a", Dir: sandbox.MemoryDir("/src/a")}, {Name: "b", Dir: sandbox.MemoryDir("/src/b")}}
	if !slices.Equal(m, want) {
		t.Errorf("memory = %+v, want %+v", m, want)
	}
}
