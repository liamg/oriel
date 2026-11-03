package runner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/sandbox"
)

func TestStaleImages(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	age := func(tag string, d time.Duration) {
		p := filepath.Join(dir, tag)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-d), now.Add(-d)); err != nil {
			t.Fatal(err)
		}
	}
	age("old", imageKeep+time.Hour)
	age("recent", time.Hour)

	got := staleImages([]string{"old", "recent", "unseen"}, dir, now)
	if !slices.Equal(got, []string{"old"}) {
		t.Errorf("stale = %v", got)
	}
	// An image nothing is known about starts its clock, and is not removed
	// until that runs out.
	if _, err := os.Stat(filepath.Join(dir, "unseen")); err != nil {
		t.Error("an unseen image was not stamped")
	}
	if got := staleImages([]string{"unseen"}, dir, now.Add(imageKeep+time.Hour)); !slices.Equal(got, []string{"unseen"}) {
		t.Errorf("after its clock ran out: %v", got)
	}
}

func TestMarkImageUsed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	markImageUsed("oriel-sandbox:abc123")
	markImageUsed("ubuntu:24.04")
	markImageUsed("my-own-image")
	ents, _ := os.ReadDir(imageStamps())
	if len(ents) != 1 || ents[0].Name() != "abc123" {
		t.Errorf("stamps = %v", ents)
	}
}

func TestOrphanAgents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	roots := map[string]string{}
	for _, n := range []string{"attached", "orphan", "launching"} {
		root := t.TempDir()
		if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
			t.Fatal(err)
		}
		roots[n] = root
	}
	old := time.Now().Add(-time.Hour)
	for _, n := range []string{"attached", "orphan"} {
		env := filepath.Join(sandbox.Dir(roots[n]), "agent.env")
		_ = os.WriteFile(env, nil, 0o600)
		_ = os.Chtimes(env, old, old)
	}
	_ = os.WriteFile(filepath.Join(sandbox.Dir(roots["launching"]), "agent.env"), nil, 0o600)
	stranger := t.TempDir()
	ps := strings.Join([]string{
		Name(roots["attached"]) + "\t" + roots["attached"],
		Name(roots["orphan"]) + "\t" + roots["orphan"],
		ProxyName(roots["orphan"]) + "\t" + roots["orphan"],
		Name(roots["launching"]) + "\t" + roots["launching"],
		Name(stranger) + "\t" + stranger,
	}, "\n")
	got := orphanAgents(ps, func(root string) bool { return root == roots["attached"] })
	if !slices.Equal(got, []string{Name(roots["orphan"])}) {
		t.Errorf("orphans = %v", got)
	}
}

func TestStaleBuilds(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var paths []string
	for i, name := range []string{"a", "b", "c", "d", "e"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		at := now.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	// "b" is old but a container runs it; "d" and "e" are the newest two.
	got := staleBuilds(paths, []string{paths[1], filepath.Join(paths[1], "inner")}, 2)
	slices.Sort(got)
	want := []string{paths[0], paths[2]}
	if !slices.Equal(got, want) {
		t.Fatalf("staleBuilds = %v, want %v", got, want)
	}
}

// A launch is marked before it touches the sidecar or the network, so a sweep while it's under way leaves them be;
// the agent's env from the last launch is kept until the launch writes its own.
func TestMarkLaunchKeepsALaunchsSidecar(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	if launchedWithin(root, launchGrace) {
		t.Fatal("never launched, but launching")
	}
	if err := markLaunch(root); err != nil || !launchedWithin(root, launchGrace) {
		t.Fatalf("a first launch isn't marked: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.WriteFile(launchStamp(root), []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(launchStamp(root), old, old)
	if launchedWithin(root, launchGrace) {
		t.Fatal("an hour-old launch counts as under way")
	}
	if err := markLaunch(root); err != nil || !launchedWithin(root, launchGrace) {
		t.Fatalf("a launch again isn't marked: %v", err)
	}
	if b, _ := os.ReadFile(launchStamp(root)); string(b) != "A=1\n" {
		t.Errorf("the last launch's env = %q", b)
	}
}
