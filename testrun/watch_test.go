package testrun

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
)

func packagesOf(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Package)
	}
	sort.Strings(out)
	return out
}

// TestGoWatchRerunsAffectedPackages: a save re-runs only the affected packages in the Go
// fixture, where c imports b, b imports a, and d imports none of them.
//
// ref: go/help-list.txt
// quote: "Deps []string // all (recursively) imported dependencies"
// ref: go/help-list.txt
// quote: "XTestImports []string // imports from XTestGoFiles"
func TestGoWatchRerunsAffectedPackages(t *testing.T) {
	root := fixtureRoot(t, "go/watch")
	ws := Workspace{Root: root, Env: []string{"GOWORK=off"}}
	g := &Go{}
	for _, c := range []struct {
		saved string
		want  []string
	}{
		{"a/a.go", []string{"example.com/watch/a", "example.com/watch/b", "example.com/watch/c"}},
		{"b/b.go", []string{"example.com/watch/b", "example.com/watch/c"}},
		{"d/d.go", []string{"example.com/watch/d"}},
		{"a/a_test.go", []string{"example.com/watch/a"}},
		{"go.mod", []string{"example.com/watch/a", "example.com/watch/b", "example.com/watch/c", "example.com/watch/d"}},
	} {
		items, err := g.Affected(ws, filepath.Join(root, c.saved))
		if err != nil {
			t.Fatal(err)
		}
		if got := packagesOf(items); !slices.Equal(got, c.want) {
			t.Errorf("saving %s selects %q, want %q", c.saved, got, c.want)
		}
		if c.saved != "a/a.go" && c.saved != "d/d.go" {
			continue
		}
		// The save re-runs those packages, and only those.
		x, err := Start(context.Background(), Request{Adapter: g, Workspace: ws, Items: items, Mode: ModeWatch, By: ByWatch, Trigger: "watch"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		run, err := x.Wait()
		if err != nil {
			t.Fatal(err)
		}
		var ran []string
		for _, p := range run.Packages {
			ran = append(ran, p.Package)
		}
		sort.Strings(ran)
		if !slices.Equal(ran, c.want) || run.Status != StatusPassed || run.By != ByWatch {
			t.Errorf("saving %s ran %q (%s by %s), want %q passed\n%s", c.saved, ran, run.Status, run.By, c.want, run.Stderr)
		}
	}
	if gr, _ := g.packageGraph(ws, false); gr.lists != 1 {
		t.Errorf("go list ran %d times, want once: the graph is cached", gr.lists)
	}
}

func TestGoWatchRelistsWhenGoModChanges(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS(fixtureRoot(t, "go/watch"))); err != nil {
		t.Fatal(err)
	}
	ws := Workspace{Root: root, Env: []string{"GOWORK=off"}}
	g := &Go{}
	if _, err := g.Affected(ws, filepath.Join(root, "a", "a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Affected(ws, filepath.Join(root, "a", "a.go")); err != nil {
		t.Fatal(err)
	}
	lists := func() int { gr, _ := g.packageGraph(ws, false); return gr.lists }
	if n := lists(); n != 1 {
		t.Fatalf("go list ran %d times, want 1", n)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/watch\n\ngo 1.22\n\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Affected(ws, filepath.Join(root, "a", "a.go")); err != nil {
		t.Fatal(err)
	}
	if n := lists(); n != 2 {
		t.Errorf("after go.mod changed, go list ran %d times, want 2", n)
	}
	// A package added since: it's listed again to find it.
	os.MkdirAll(filepath.Join(root, "e"), 0o755)
	os.WriteFile(filepath.Join(root, "e", "e.go"), []byte("package e\n\nimport \"example.com/watch/d\"\n\nvar X = d.Hello()\n"), 0o644)
	os.WriteFile(filepath.Join(root, "e", "e_test.go"), []byte("package e\n\nimport \"testing\"\n\nfunc TestE(t *testing.T) {}\n"), 0o644)
	items, err := g.Affected(ws, filepath.Join(root, "e", "e.go"))
	if err != nil {
		t.Fatal(err)
	}
	if got := packagesOf(items); !slices.Equal(got, []string{"example.com/watch/e"}) {
		t.Errorf("new package selects %q", got)
	}
	items, _ = g.Affected(ws, filepath.Join(root, "d", "d.go"))
	if got := packagesOf(items); !slices.Equal(got, []string{"example.com/watch/d", "example.com/watch/e"}) {
		t.Errorf("d now selects %q", got)
	}
}
