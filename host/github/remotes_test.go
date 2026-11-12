package github

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/gitx/gittest"
)

// A checkout's origin is read with git once, then again only when its config changes.
func TestRemoteURLReadOnceUntilTheConfigChanges(t *testing.T) {
	r := gittest.New(t)
	set := func(url string) {
		t.Helper()
		if out, err := exec.Command("git", "-C", r.Dir, "remote", "add", "origin", url).CombinedOutput(); err != nil {
			if out, err = exec.Command("git", "-C", r.Dir, "remote", "set-url", "origin", url).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		}
	}
	set("https://github.com/acme/search-api.git")
	var x GitHub
	ctx := context.Background()
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search-api.git" {
		t.Fatalf("origin %q", got)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // git can't be run: only the kept one answers
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search-api.git" {
		t.Errorf("read again, with git gone: %q", got)
	}
	t.Setenv("PATH", path)
	config := filepath.Join(r.Dir, ".git", "config")
	fi, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	set("https://github.com/acme/search.git")
	later := fi.ModTime().Add(time.Minute) // a coarse clock may have left the config's time where it was
	if err := os.Chtimes(config, later, later); err != nil {
		t.Fatal(err)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search.git" {
		t.Errorf("after the remote moved: %q", got)
	}
}

// A read of the origin that fails (git couldn't run) isn't kept as "no origin": the next read asks git again.
func TestAFailedRemoteReadIsntKept(t *testing.T) {
	r := gittest.New(t)
	if out, err := exec.Command("git", "-C", r.Dir, "remote", "add", "origin", "https://github.com/acme/search-api.git").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var x GitHub
	ctx := context.Background()
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "" {
		t.Fatalf("with git gone: %q", got)
	}
	t.Setenv("PATH", path)
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search-api.git" {
		t.Errorf("with git back, the config unchanged: %q", got)
	}
}

// "No origin" isn't kept: a repo that read as having none is asked again, so one bad read (a worktree caught as it's
// made, say) never sticks, even with the config's time where it was.
func TestNoOriginIsAskedAgain(t *testing.T) {
	r := gittest.New(t)
	var x GitHub
	ctx := context.Background()
	config := filepath.Join(r.Dir, ".git", "config")
	fi, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "" {
		t.Fatalf("origin %q", got)
	}
	if out, err := exec.Command("git", "-C", r.Dir, "remote", "add", "origin", "https://github.com/acme/search-api.git").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if err := os.Chtimes(config, fi.ModTime(), fi.ModTime()); err != nil { // the change landed in the same tick
		t.Fatal(err)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search-api.git" {
		t.Errorf("read again: %q", got)
	}
}

// A fresh read (Read again, a fetch) asks git whatever was kept: an origin that moved within its config's clock
// tick is found.
func TestAFreshReadAsksAgain(t *testing.T) {
	r := gittest.New(t)
	r.Git("remote", "add", "origin", "https://github.com/acme/search-api.git")
	var x GitHub
	ctx := context.Background()
	config := filepath.Join(r.Dir, ".git", "config")
	fi, err := os.Stat(config)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search-api.git" {
		t.Fatalf("origin %q", got)
	}
	r.Git("remote", "set-url", "origin", "https://github.com/acme/search.git")
	if err := os.Chtimes(config, fi.ModTime(), fi.ModTime()); err != nil { // the change landed in the same tick
		t.Fatal(err)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search-api.git" {
		t.Fatalf("a kept read went to git: %q", got)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, true); got != "https://github.com/acme/search.git" {
		t.Errorf("a fresh read: %q", got)
	}
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}, false); got != "https://github.com/acme/search.git" {
		t.Errorf("after the fresh read, kept: %q", got)
	}
}
