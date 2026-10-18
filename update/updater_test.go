package update_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/update"
)

// The app's Updater: it finds a newer release and installs this copy.
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

	if _, err := os.Stat(filepath.Join(u.CacheDir, "2.0.0", "oriel-linux-amd64")); err != nil {
		t.Errorf("the download wasn't cached: %v", err)
	}
}
