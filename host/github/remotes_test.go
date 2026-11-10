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
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}); got != "https://github.com/acme/search-api.git" {
		t.Fatalf("origin %q", got)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // git can't be run: only the kept one answers
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}); got != "https://github.com/acme/search-api.git" {
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
	if got := x.remoteURL(ctx, gitx.Repo{Dir: r.Dir}); got != "https://github.com/acme/search.git" {
		t.Errorf("after the remote moved: %q", got)
	}
}
