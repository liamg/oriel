package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/toml"
)

// An id names a directory under the plugins dir, so one that climbs out of it is refused, in [plugins] and in the
// store.
func TestIDsStayInThePluginsDir(t *testing.T) {
	doc, err := toml.Parse([]byte("[plugins]\n\"../..\" = \"*\"\n\"x/..\" = \"*\"\n\"./x\" = \"*\"\n\"acme/jira\" = \"*\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	es, errs := ParseEntries(doc.Value()["plugins"].(map[string]any))
	if len(es) != 1 || es[0].ID != "acme/jira" || len(errs) != 3 {
		t.Errorf("entries %+v, errors %v", es, errs)
	}

	root := t.TempDir()
	keep := filepath.Join(root, "keep")
	os.WriteFile(keep, []byte("mine"), 0o644)
	s := Store{Dir: filepath.Join(root, "data", "plugins")}
	os.MkdirAll(s.Dir, 0o755)
	for _, id := range []string{"../..", "../data", "x/..", ".."} {
		if err := s.Remove(id); err == nil {
			t.Errorf("Remove(%q) is allowed", id)
		}
		if _, err := s.FetchArchive(id, commitA, filepath.Join(root, "none.tar.gz")); err == nil || !strings.Contains(err.Error(), "isn't <owner>/<name>") {
			t.Errorf("FetchArchive(%q): %v", id, err)
		}
	}
	if _, err := s.FetchArchive("acme/jira", "../../x", filepath.Join(root, "none.tar.gz")); err == nil || !strings.Contains(err.Error(), "commit") {
		t.Errorf("a commit that climbs out: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a file outside the plugins dir is gone: %v", err)
	}
	if _, err := os.Stat(s.Dir); err != nil {
		t.Errorf("the plugins dir is gone: %v", err)
	}
}
