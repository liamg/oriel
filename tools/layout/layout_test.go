package layout

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func root(t *testing.T) string {
	wd, _ := os.Getwd()
	r, err := Root(wd)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDesignModulesExist(t *testing.T) {
	r := root(t)
	mods, err := DesignModules(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) < 20 {
		t.Fatalf("found %d modules in architecture.md, the table parser is broken", len(mods))
	}
	for _, m := range mods {
		if fi, err := os.Stat(filepath.Join(r, m)); err != nil || !fi.IsDir() {
			t.Errorf("architecture.md names %s, but there is no such directory", m)
		}
	}
}

func TestWorkspaceListsEveryModule(t *testing.T) {
	r := root(t)
	work, err := WorkModules(r)
	if err != nil {
		t.Fatal(err)
	}
	dirs, err := ModuleDirs(r)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(work)
	slices.Sort(dirs)
	if !slices.Equal(work, dirs) {
		t.Errorf("go.work uses %q\nbut go.mod files are in %q", work, dirs)
	}
}
