package ui

import "testing"

// Path is Child down each key in turn.
func TestPath(t *testing.T) {
	root := ID(42)
	if got, want := Path(root, "box", "head", "input"), Child(Child(Child(root, "box"), "head"), "input"); got != want {
		t.Errorf("Path = %v, want %v", got, want)
	}
	if Path(root) != root {
		t.Error("no keys isn't the parent")
	}
}
