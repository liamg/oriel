package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/update"
)

// A version is released only once, and above every version before it: release.yml's preflight refuses one that
// isn't, before anything is built. A pre-release comes before its release, as semver.org orders them.
func TestANewVersionIsAboveEveryOneSoFar(t *testing.T) {
	tags := []string{"v0.0.12", "v0.0.13", "v0.1.0-rc.1", "not-a-version", "1.0.0"}
	for v, ok := range map[string]bool{
		"v0.1.0":      true,  // the release after its pre-release
		"v0.1.0-rc.2": true,  // a later pre-release
		"v0.0.14":     false, // below v0.1.0-rc.1
		"v0.1.0-rc.1": false, // already made
		"v0.0.13":     false,
		"0.2.0":       false, // no v
		"v0.2":        false, // not a full version
	} {
		if err := newVersion(v, tags); (err == nil) != ok {
			t.Errorf("newVersion(%q) = %v, want ok %v", v, err, ok)
		}
	}
}

// What release.yml publishes is a release the app's updater accepts: signed by the key whose public half the build
// trusts, listing each Linux archive, binary and macOS bundle by platform and kind with its size and sha256. A DMG is
// uploaded but isn't the updater's, and neither is another version's archive.
func TestReleaseIsWhatTheUpdaterVerifies(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	files := map[string]string{"oriel-0.5.0-rc.1-linux-amd64.tar.gz": "linux", "oriel-0.5.0-rc.1-linux-arm64.tar.gz": "arm",
		"oriel-0.4.0-linux-amd64.tar.gz": "skipped", "oriel-0.5.0-rc.1-linux.tar.gz": "skipped", "oriel-darwin-arm64": "mach-o", "oriel-darwin-arm64.app.tar.gz": "bundle",
		"Oriel-0.5.0-rc.1-arm64.dmg": "skipped", "oriel-darwin-arm64.dmg": "skipped", "notes.txt": "skipped"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(in, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seed := make([]byte, ed25519.SeedSize)
	key, err := parseKey(hex.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	if err := run("v0.5.0-rc.1", in, out, "", key, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	rel, err := update.Dir(out).Latest(context.Background(), update.Nightly)
	if err != nil {
		t.Fatal(err)
	}
	m, err := rel.Verify([]ed25519.PublicKey{key.Public().(ed25519.PublicKey)})
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "v0.5.0-rc.1" || m.Channel != update.Nightly || len(m.Artifacts) != 4 {
		t.Fatalf("manifest = %+v", m)
	}
	want := map[[2]string]string{
		{"linux/amd64", update.KindArchive}: "oriel-0.5.0-rc.1-linux-amd64.tar.gz",
		{"linux/arm64", update.KindArchive}: "oriel-0.5.0-rc.1-linux-arm64.tar.gz",
		{"darwin/arm64", update.KindBinary}: "oriel-darwin-arm64",
		{"darwin/arm64", update.KindBundle}: "oriel-darwin-arm64.app.tar.gz",
	}
	for k, name := range want {
		a, ok := m.Artifact(k[0], k[1])
		if !ok || a.Name != name {
			t.Errorf("%s %s = %+v, %v; want %s", k[0], k[1], a, ok, name)
			continue
		}
		if b, err := os.ReadFile(filepath.Join(out, a.Name)); err != nil || int64(len(b)) != a.Size || string(b) != files[name] {
			t.Errorf("%s: %v, %+v", a.Name, err, a)
		}
	}
	if _, err := parseKey("abcd"); err == nil {
		t.Error("a short key was taken")
	}
	if err := run("0.5.0", in, out, "", key, time.Now()); err == nil {
		t.Error("a version that isn't a v-tag was released")
	}
}
