package attr

import (
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixture copies testdata/tree into a fresh git repository, with $GIT_DIR/info/attributes, and returns its root.
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	err := filepath.WalkDir("testdata/tree", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel("testdata/tree", p)
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755)
		return os.WriteFile(filepath.Join(root, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q")
	os.WriteFile(filepath.Join(root, ".git", "info", "attributes"), []byte("plain eol=crlf\n*.js -linguist-vendored\n"), 0o644)
	return root
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func format(v Value) string {
	switch {
	case v.Set:
		return "set"
	case v.Unset:
		return "unset"
	}
	return v.Text
}

// Every path in the fixture gets what `git check-attr -a` says, the oracle: patterns as gitignore(5) has them,
// precedence by directory and $GIT_DIR/info/attributes, macros and the built-in binary.
// ref:   oracle git check-attr; gitattributes(5), https://git-scm.com/docs/gitattributes
func TestMatchesGitCheckAttr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	root := fixture(t)
	var paths []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() != ".gitattributes" {
			rel, _ := filepath.Rel(root, p)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	want := map[string]map[string]string{}
	for _, l := range strings.Split(git(t, root, append([]string{"check-attr", "-a", "--"}, paths...)...), "\n") {
		if l == "" {
			continue
		}
		// "path: attr: value", where path may hold ": " no more than our fixture's names do.
		p, rest, _ := strings.Cut(l, ": ")
		a, v, _ := strings.Cut(rest, ": ")
		if want[p] == nil {
			want[p] = map[string]string{}
		}
		want[p][a] = v
	}
	r := Open(root)
	for _, p := range paths {
		got := map[string]string{}
		attrs, err := r.Attributes(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		for k, v := range attrs {
			got[k] = format(v)
		}
		if !maps.Equal(got, want[p]) {
			t.Errorf("%s:\n got %v\nwant %v", p, sorted(got), sorted(want[p]))
		}
	}
}

func sorted(m map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out = append(out, k+"="+m[k])
	}
	return out
}

// Where each value came from, for the settings page.
func TestSources(t *testing.T) {
	root := fixture(t)
	attrs, _ := Open(root).Attributes("src/main.go")
	if v := attrs["eol"]; v.Text != "crlf" || v.File != filepath.Join(root, "src", ".gitattributes") || v.Line != 1 {
		t.Errorf("eol: %+v", v)
	}
}
