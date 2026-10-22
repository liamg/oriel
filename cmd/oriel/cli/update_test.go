package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/update"
)

func TestAppBundle(t *testing.T) {
	b, inner, ok := appBundle("/Applications/Oriel.app/Contents/MacOS/oriel")
	if !ok || b != "/Applications/Oriel.app" || inner != "Contents/MacOS/oriel" {
		t.Errorf("appBundle = %q, %q, %v", b, inner, ok)
	}
	if _, _, ok := appBundle("/usr/local/bin/oriel"); ok {
		t.Error("a plain binary is a bundle")
	}
}

// What a copy updates from: Oriel.app from its bundle, a Linux binary from the release's archive, a Mac binary alone.
func TestInstallerFor(t *testing.T) {
	for _, c := range []struct{ exe, goos, target, kind string }{
		{"/Applications/Oriel.app/Contents/MacOS/oriel", "darwin", "/Applications/Oriel.app", update.KindBundle},
		{"/usr/local/bin/oriel", "darwin", "/usr/local/bin/oriel", update.KindBinary},
		{"/home/u/.local/bin/oriel", "linux", "/home/u/.local/bin/oriel", update.KindArchive},
	} {
		in, kind := installerFor(c.exe, c.goos, "/state/updates")
		if in.Target != c.target || kind != c.kind || in.Dir != "/state/updates" {
			t.Errorf("installerFor(%q, %s) = %+v, %s; want %s, %s", c.exe, c.goos, in, kind, c.target, c.kind)
		}
	}
}

// `oriel update` in a build CI didn't stamp says it can't, rather than checking.
func TestUpdateNeedsAReleaseKey(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"update", "--check"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no release key") {
		t.Errorf("oriel update = %d, %q", code, errOut.String())
	}
}

// `oriel update` checks, then installs, then says it's waiting for a restart.
func TestUpdateSelf(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	dir, root := t.TempDir(), t.TempDir()
	body := []byte("oriel 2.0.0")
	a := update.Describe("oriel", update.Platform(), update.KindBinary, body)
	rel, err := update.Sign(key, update.Manifest{Version: "2.0.0", Artifacts: []update.Artifact{a}})
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Write(dir, rel, map[string][]byte{"oriel": body}); err != nil {
		t.Fatal(err)
	}
	in := &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"),
		Probe: func(context.Context, string, string) error { return nil }}
	if err := os.WriteFile(in.Target, []byte("oriel 1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &update.Updater{Source: update.Dir(dir), Keys: []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, Version: "1.0.0",
		Installer: in, CacheDir: t.TempDir()}
	ctx := context.Background()
	for _, step := range []struct {
		check bool
		want  string
	}{
		{true, "Oriel 2.0.0 is out (you have 1.0.0)"},
		{false, "Oriel 2.0.0 is installed: restart Oriel to run it"},
		{false, "Oriel 2.0.0 is installed: restart Oriel to run it"},
	} {
		var out bytes.Buffer
		if err := updateSelf(ctx, u, update.Stable, step.check, &out); err != nil || !strings.Contains(out.String(), step.want) {
			t.Fatalf("check=%v: %q, %v; want %q", step.check, out.String(), err, step.want)
		}
	}
	if b, _ := os.ReadFile(in.Target); string(b) != string(body) {
		t.Fatalf("installed %q", b)
	}
}

// A second Oriel, refused the lock, leaves the first's pending update alone: only the one holding it runs Boot.
func TestBootRunsOnlyUnderTheLock(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	in := &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"),
		Probe: func(context.Context, string, string) error { return nil }}
	if err := os.WriteFile(in.Target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte("new")
	a := update.Describe("oriel", update.Platform(), update.KindBinary, body)
	if err := in.Stage(context.Background(), update.Manifest{Version: "2.0.0"}, a, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	if err := in.Swap(); err != nil {
		t.Fatal(err)
	}
	first, err := lock(home)
	if err != nil {
		t.Fatal(err)
	}
	var cfg app.Config
	if err := claim(home, &cfg, in); !errors.Is(err, ErrRunning) {
		t.Fatalf("claim with the lock held = %v", err)
	}
	if p, ok := in.Pending(); !ok || p.Boots != 0 {
		t.Fatalf("a refused start booted the update: %+v, %v", p, ok)
	}
	first.Close()

	if err := claim(home, &cfg, in); err != nil {
		t.Fatal(err)
	}
	defer unlock()
	cfg.Started()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := in.Pending(); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the start holding the lock never confirmed the update")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if b, _ := os.ReadFile(in.Target); string(b) != "new" {
		t.Errorf("Target = %q", b)
	}
}

type closer struct{ closed *[]string }

func (c closer) Close() error { *c.closed = append(*c.closed, "logs"); return nil }

// The log is closed, after everything that may still log, before the app exits.
func TestShutdownClosesTheLogLast(t *testing.T) {
	var got []string
	stop := func() { got = append(got, "host") }
	if code := shutdown(nil, closer{&got}, stop); code != 0 || strings.Join(got, ",") != "host,logs" {
		t.Errorf("shutdown = %d, %q", code, got)
	}
	got = nil
	if code := shutdown(errors.New("boom"), closer{&got}, stop); code != 1 || strings.Join(got, ",") != "host,logs" {
		t.Errorf("shutdown with an error = %d, %q", code, got)
	}
}
