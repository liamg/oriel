package pathx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInside(t *testing.T) {
	for _, c := range []struct {
		path, dir      string
		inside, strict bool
	}{
		{"/a/b", "/a", true, true},
		{"/a", "/a", true, false},
		{"/a/", "/a", true, false},
		{"/ab", "/a", false, false},
		{"/a/../b", "/a", false, false},
		{"/a/b/../c", "/a", true, true},
		{"/b", "/a", false, false},
		{"", "/a", false, false},
		{"/a", "", false, false},
		{"rel/x", "rel", true, true},
	} {
		if got := Inside(c.path, c.dir); got != c.inside {
			t.Errorf("Inside(%q, %q) = %v", c.path, c.dir, got)
		}
		if got := StrictlyInside(c.path, c.dir); got != c.strict {
			t.Errorf("StrictlyInside(%q, %q) = %v", c.path, c.dir, got)
		}
	}
}

func TestInsideResolved(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if !InsideResolved(filepath.Join(link, "sub"), real) || Inside(filepath.Join(link, "sub"), real) {
		t.Error("a path through a link to dir is inside it resolved, and only resolved")
	}
	if InsideResolved(filepath.Join(dir, "other"), real) {
		t.Error("a path elsewhere is inside")
	}
}
