package plugin

import "testing"

// An id is <owner>/<name>, and it names a directory under the plugins dir, so it can't climb out of it.
func TestCheckID(t *testing.T) {
	for _, id := range []string{"acme/jira", "liamg/oriel-surround", "a.b/c_d"} {
		if err := CheckID(id); err != nil {
			t.Errorf("%q: %v", id, err)
		}
	}
	for _, id := range []string{"", "acme", "/jira", "acme/", "a/b/c", "../..", "../x", "x/..", "./x", "x/.", "/abs", "a//b"} {
		if err := CheckID(id); err == nil {
			t.Errorf("%q is accepted", id)
		}
		if _, err := ParseManifest([]byte("id = \"" + id + "\"\n")); err == nil {
			t.Errorf("a manifest with id %q is read", id)
		}
	}
}
