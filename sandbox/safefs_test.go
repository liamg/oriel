package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func sandboxed(t *testing.T) (root, outside string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root = filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, ModeSandbox); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestReadFileRefusesALinkOut(t *testing.T) {
	root, outside := sandboxed(t)
	link := filepath.Join(root, "exclude")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFile(link); err == nil {
		t.Fatalf("read through a link out of the workspace: %q", b)
	}
	if f, err := Open(link); err == nil {
		_ = f.Close()
		t.Fatal("opened through a link out of the workspace")
	}
}

func TestReadFileAllowsLinksInside(t *testing.T) {
	root, _ := sandboxed(t)
	real := filepath.Join(root, "a.txt")
	if err := os.WriteFile(real, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFile(filepath.Join(root, "b.txt")); err != nil || string(b) != "inside" {
		t.Fatalf("got %q, %v", b, err)
	}
}

func TestReadFileOutsideSandboxesFollowsLinks(t *testing.T) {
	_, outside := sandboxed(t)
	plain := t.TempDir()
	link := filepath.Join(plain, "l")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFile(link); err != nil || string(b) != "host secret" {
		t.Fatalf("a plain workspace should behave as before: %q, %v", b, err)
	}
}

func TestWriteFileReplacesALinkRatherThanWritingThrough(t *testing.T) {
	root, outside := sandboxed(t)
	link := filepath.Join(root, "run.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteIn(root, link, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "host secret" {
		t.Fatalf("wrote through the link: %q", b)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the link is still there")
	}
}

func TestWriteFileRefusesALinkedDirectory(t *testing.T) {
	root, outside := sandboxed(t)
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "dir")); err != nil {
		t.Fatal(err)
	}
	if err := WriteIn(root, filepath.Join(root, "dir", "x"), []byte("x"), 0o644); err == nil {
		t.Fatal("wrote into a linked directory")
	}
}

func TestAlwaysEverywhereQueuesForTheManager(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := AddAlwaysRules([]PolicyRule{{Tool: "a", Action: "allow"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := AddAlwaysRules([]PolicyRule{{Tool: "b", Action: "allow"}}, true); err != nil {
		t.Fatal(err)
	}
	if err := AddAlwaysEgress(EgressRule{Host: "x", Port: 443, Allow: true}, true); err != nil {
		t.Fatal(err)
	}
	a := LoadAlways()
	if len(a.Rules) != 2 || len(a.Shared) != 1 || a.Shared[0].Tool != "b" || len(a.SharedEgress) != 1 {
		t.Fatalf("always = %+v", a)
	}
	if err := ClearShared(a.Shared, a.SharedEgress); err != nil {
		t.Fatal(err)
	}
	a = LoadAlways()
	if len(a.Shared) != 0 || len(a.SharedEgress) != 0 || len(a.Rules) != 2 || len(a.Egress) != 1 {
		t.Fatalf("after clearing: %+v", a)
	}
}

func TestSetNoSignIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for range 2 {
		if err := SetNoSignIn("linear", true); err != nil {
			t.Fatal(err)
		}
	}
	if got := LoadAlways().NoSignIn; len(got) != 1 || got[0] != "linear" {
		t.Fatalf("NoSignIn = %v, want [linear]", got)
	}
	if err := SetNoSignIn("linear", false); err != nil {
		t.Fatal(err)
	}
	if got := LoadAlways().NoSignIn; len(got) != 0 {
		t.Fatalf("NoSignIn = %v, want none", got)
	}
}
