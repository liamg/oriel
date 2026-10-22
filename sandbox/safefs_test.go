package sandbox

import (
	"os"
	"path/filepath"
	"sync"
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

// flip swaps path between a real entry (made by mk) and a link to target until stop closes.
func flip(t *testing.T, path, target string, mk func() error, stop chan struct{}) *sync.WaitGroup {
	t.Helper()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tmp := path + ".link"
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Symlink(target, tmp)
			_ = os.RemoveAll(path)
			_ = os.Rename(tmp, path)
			_ = os.Remove(path)
			_ = mk()
		}
	}()
	return &wg
}

func TestReadFileRefusesALinkSwappedIn(t *testing.T) {
	root, outside := sandboxed(t)
	f := filepath.Join(root, "f")
	stop := make(chan struct{})
	wg := flip(t, f, outside, func() error { return os.WriteFile(f, []byte("inside"), 0o644) }, stop)
	defer wg.Wait()
	defer close(stop)
	for i := range 20000 {
		if b, err := ReadFile(f); err == nil && string(b) == "host secret" {
			t.Fatalf("read through a link swapped in after %d tries", i)
		}
	}
}

func TestWriteFileRefusesADirectorySwappedForALink(t *testing.T) {
	root, outside := sandboxed(t)
	away := filepath.Dir(outside)
	dir := filepath.Join(root, "dir")
	stop := make(chan struct{})
	wg := flip(t, dir, away, func() error { return os.Mkdir(dir, 0o755) }, stop)
	for range 20000 {
		_ = WriteFile(root, filepath.Join(dir, "x"), []byte("x"), 0o644)
	}
	close(stop)
	wg.Wait()
	ents, _ := os.ReadDir(away)
	if len(ents) != 1 {
		t.Fatalf("wrote outside the workspace through a swapped link: %d entries", len(ents))
	}
}
