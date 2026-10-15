package update_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/update"
	"github.com/liamg/oriel/wire"
)

// The app's Updater: it finds a newer release, installs this copy, pushes the release to an older worker, and leaves
// alone a worker that's current and one that doesn't take updates.
func TestUpdater(t *testing.T) {
	key := testKey(t)
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	body := []byte("oriel 2.0.0")
	dir := t.TempDir()
	rel := signed(t, key, "2.0.0", update.Describe("oriel-linux-amd64", "linux/amd64", update.KindBinary, body))
	if err := update.Write(dir, rel, map[string][]byte{"oriel-linux-amd64": body}); err != nil {
		t.Fatal(err)
	}
	self := installer(t)
	u := &update.Updater{Source: update.Dir(dir), Keys: trust, Version: "1.0.0", Installer: self, Platform: "linux/amd64",
		CacheDir: t.TempDir()}
	ctx := context.Background()

	if err := u.Install(ctx); !errors.Is(err, update.ErrNoRelease) {
		t.Fatalf("Install before a check = %v", err)
	}
	m, err := u.Check(ctx, update.Stable)
	if err != nil || m.Version != "2.0.0" {
		t.Fatalf("Check = %+v, %v", m, err)
	}
	if _, ok := u.Ready(); ok {
		t.Fatal("ready before installing")
	}
	if err := u.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if v, ok := u.Ready(); !ok || v != "2.0.0" {
		t.Fatalf("Ready = %q, %v", v, ok)
	}
	if b, _ := os.ReadFile(self.Target); string(b) != string(body) {
		t.Fatalf("installed %q", b)
	}

	worker := func(version string) (wire.Conn, *update.Installer, *bool) {
		a, b := wire.Pipe()
		t.Cleanup(func() { a.Close() })
		in := installer(t)
		restarted := new(bool)
		(&update.Receiver{Installer: in, Keys: trust, Version: version, Platform: "linux/amd64",
			Restart: func() { *restarted = true }}).Serve(b)
		u.Serve(a)
		return a, in, restarted
	}
	old, in, _ := worker("1.5.0")
	p, err := u.PushTo(ctx, old)
	if err != nil || p.From != "1.5.0" || p.To != "2.0.0" {
		t.Fatalf("PushTo an old worker = %+v, %v", p, err)
	}
	if pend, ok := in.Pending(); !ok || pend.Version != "2.0.0" {
		t.Fatalf("the worker's pending swap = %+v, %v", pend, ok)
	}
	if b, _ := os.ReadFile(in.Target); string(b) != string(body) {
		t.Fatalf("the worker has %q", b)
	}
	current, _, _ := worker("2.0.0")
	if p, err := u.PushTo(ctx, current); err != nil || p != (update.Pushed{}) {
		t.Fatalf("PushTo a current worker = %+v, %v", p, err)
	}
	plain, _ := wire.Pipe()
	defer plain.Close()
	if p, err := u.PushTo(ctx, plain); err != nil || p != (update.Pushed{}) {
		t.Fatalf("PushTo a worker without the updater = %+v, %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(u.CacheDir, "2.0.0", "oriel-linux-amd64")); err != nil {
		t.Errorf("the download wasn't cached: %v", err)
	}
}
