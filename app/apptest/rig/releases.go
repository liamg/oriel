package rig

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/liamg/oriel/update"
)

// Releases are signed releases in a directory, and an installed copy of Oriel 1.0.0 in another, for an updater.
type Releases struct {
	t          *testing.T
	key        ed25519.PrivateKey
	trust      []ed25519.PublicKey
	Src        string
	Self       *update.Installer
	Relaunched atomic.Int32 // how many times Relaunch ran
}

func NewReleases(t *testing.T) *Releases {
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &Releases{t: t, key: key, trust: []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, Src: t.TempDir()}
	root := t.TempDir()
	r.Self = &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"),
		Probe: func(context.Context, string, string) error { return nil }}
	if err := os.WriteFile(r.Self.Target, []byte("oriel 1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

// Publish puts version out on the stable channel, with notes.
func (r *Releases) Publish(version, notes string) []byte {
	r.t.Helper()
	body := []byte("oriel " + version)
	a := update.Describe("oriel-"+strings.ReplaceAll(update.Platform(), "/", "-"), update.Platform(), update.KindBinary, body)
	rel, err := update.Sign(r.key, update.Manifest{Version: version, Channel: update.Stable, Notes: notes, Artifacts: []update.Artifact{a}})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := update.Write(r.Src, rel, map[string][]byte{a.Name: body}); err != nil {
		r.t.Fatal(err)
	}
	return body
}

// Updater updates the installed copy from the releases.
func (r *Releases) Updater() *update.Updater {
	return &update.Updater{Source: update.Dir(r.Src), Keys: r.trust, Version: "1.0.0", Installer: r.Self, CacheDir: r.t.TempDir()}
}

// Relaunch counts a relaunch.
func (r *Releases) Relaunch() error { r.Relaunched.Add(1); return nil }
