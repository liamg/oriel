package update_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

// Download fetches ahead, so Install only verifies what it has; each says how far it's got, stage by stage, and a
// download counts its bytes up to the artifact's size.
func TestUpdaterReportsEachStep(t *testing.T) {
	key := testKey(t)
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	body := make([]byte, 100_000)
	dir := t.TempDir()
	rel := signed(t, key, "2.0.0", update.Describe("oriel-linux-amd64", "linux/amd64", update.KindBinary, body))
	if err := update.Write(dir, rel, map[string][]byte{"oriel-linux-amd64": body}); err != nil {
		t.Fatal(err)
	}
	var steps []update.Step
	u := &update.Updater{Source: update.Dir(dir), Keys: trust, Version: "1.0.0", Installer: installer(t), Platform: "linux/amd64",
		CacheDir: t.TempDir(), Progress: func(s update.Step) { steps = append(steps, s) }}
	ctx := context.Background()
	if _, err := u.Check(ctx, update.Stable); err != nil {
		t.Fatal(err)
	}
	if err := u.Download(ctx); err != nil {
		t.Fatal(err)
	}
	last := steps[len(steps)-2]
	if last.Stage != update.StageDownload || last.Done != last.Total || last.Total != int64(len(body)) {
		t.Errorf("the download's last count: %+v", last)
	}
	if steps[len(steps)-1].Stage != update.StageVerify {
		t.Errorf("the download wasn't verified: %+v", steps[len(steps)-1])
	}

	steps = nil
	if err := u.Install(ctx); err != nil {
		t.Fatal(err)
	}
	var stages []string
	for _, s := range steps {
		stages = append(stages, s.Stage)
	}
	want := []string{update.StageVerify, update.StageExtract, update.StageCheck, update.StageSwap}
	if !slices.Equal(stages, want) {
		t.Errorf("install's stages = %v, want %v (nothing downloaded again)", stages, want)
	}
}

// A Linux copy installs from the release's oriel-<version>-linux-<arch>.tar.gz: the binary inside it replaces this
// one, and the archive's other files (the .desktop file, the icon, the notices) aren't installed.
func TestUpdaterInstallsTheBinaryFromALinuxArchive(t *testing.T) {
	key := testKey(t)
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	exe := []byte("#!/bin/sh\necho oriel 2.0.0\n")
	archive := tarGz(t, []tarFile{
		{name: "oriel-2.0.0-linux-amd64/", dir: true},
		{name: "oriel-2.0.0-linux-amd64/oriel", body: exe, mode: 0o755},
		{name: "oriel-2.0.0-linux-amd64/oriel.desktop", body: []byte("[Desktop Entry]\n"), mode: 0o644},
		{name: "oriel-2.0.0-linux-amd64/THIRD_PARTY_NOTICES", body: []byte("notices"), mode: 0o644},
	})
	name := "oriel-2.0.0-linux-amd64.tar.gz"
	dir := t.TempDir()
	rel := signed(t, key, "2.0.0",
		update.Describe(name, "linux/amd64", update.KindArchive, archive),
		update.Describe("oriel-darwin-arm64", "darwin/arm64", update.KindBinary, []byte("mach-o")))
	if err := update.Write(dir, rel, map[string][]byte{name: archive}); err != nil {
		t.Fatal(err)
	}
	self := installer(t)
	u := &update.Updater{Source: update.Dir(dir), Keys: trust, Version: "1.0.0", Installer: self, Platform: "linux/amd64",
		Kind: update.KindArchive, CacheDir: t.TempDir()}
	ctx := context.Background()
	if _, err := u.Check(ctx, update.Stable); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(self.Target); string(b) != string(exe) {
		t.Fatalf("installed %q", b)
	}
	if fi, err := os.Stat(self.Target); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("the installed binary isn't executable: %v %v", fi.Mode(), err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(self.Target)); slices.ContainsFunc(entries, func(e os.DirEntry) bool {
		return e.Name() == "oriel.desktop" || e.Name() == "THIRD_PARTY_NOTICES"
	}) {
		t.Error("the archive's other files were installed beside the binary")
	}
}

func TestAnArchiveWithoutTheBinaryIsRefused(t *testing.T) {
	for name, files := range map[string][]tarFile{
		"missing":   {{name: "oriel-2.0.0-linux-amd64/README", body: []byte("x"), mode: 0o644}},
		"directory": {{name: "oriel-2.0.0-linux-amd64/oriel/", dir: true}},
		"too deep":  {{name: "a/b/oriel", body: []byte("x"), mode: 0o755}},
	} {
		in := installer(t)
		body := tarGz(t, files)
		a := update.Describe("oriel-2.0.0-linux-amd64.tar.gz", update.Platform(), update.KindArchive, body)
		if err := in.Stage(context.Background(), update.Manifest{Version: "2.0.0"}, a, bytes.NewReader(body)); err == nil {
			t.Errorf("%s: an archive with no oriel at its top was staged", name)
		}
	}
}
