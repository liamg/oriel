package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/liamg/oriel/semver"
)

// Artifact kinds: the executable alone, a macOS .app bundle as a .tar.gz, or Linux's .tar.gz holding the executable
// (with its .desktop file, icon and notices, which the updater leaves alone).
const (
	KindBinary  = "binary"
	KindBundle  = "bundle"
	KindArchive = "archive"
)

// Channels a release is published on.
const (
	Stable  = "stable"
	Nightly = "nightly"
)

// Manifest is what CI signs: the release's version and every artifact's checksum.
type Manifest struct {
	Version   string     `json:"version"`
	Channel   string     `json:"channel,omitempty"`
	Published time.Time  `json:"published,omitzero"`
	Notes     string     `json:"notes,omitempty"` // the release's markdown
	Artifacts []Artifact `json:"artifacts"`
}

// Artifact is one downloadable file of a release.
type Artifact struct {
	Name     string `json:"name"`     // the file's name in the release, e.g. oriel-0.5.0-linux-amd64.tar.gz
	Platform string `json:"platform"` // GOOS/GOARCH
	Kind     string `json:"kind"`     // KindBinary, KindBundle or KindArchive
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"` // hex
}

// Release is a manifest as published: its exact bytes and their ed25519 signature.
type Release struct {
	Manifest  []byte `json:"manifest"`
	Signature []byte `json:"signature"`
}

// Platform is this process's GOOS/GOARCH.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Sign makes a release of m with key, the way CI does.
func Sign(key ed25519.PrivateKey, m Manifest) (Release, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Release{}, err
	}
	return Release{Manifest: b, Signature: ed25519.Sign(key, b)}, nil
}

// ErrUnsigned is a release no trusted key signed.
var ErrUnsigned = errors.New("the release's signature does not verify")

// Verify checks the release's signature against each trusted key and returns its manifest. Nothing in an
// unverified manifest is read.
func (r Release) Verify(keys []ed25519.PublicKey) (Manifest, error) {
	if len(keys) == 0 {
		return Manifest{}, errors.New("this build trusts no release key")
	}
	ok := false
	for _, k := range keys {
		if len(k) == ed25519.PublicKeySize && ed25519.Verify(k, r.Manifest, r.Signature) {
			ok = true
			break
		}
	}
	if !ok {
		return Manifest{}, ErrUnsigned
	}
	var m Manifest
	if err := json.Unmarshal(r.Manifest, &m); err != nil {
		return Manifest{}, fmt.Errorf("the release's manifest: %w", err)
	}
	if m.Version == "" {
		return Manifest{}, errors.New("the release's manifest has no version")
	}
	return m, nil
}

// Artifact is the release's file for a platform and kind.
func (m Manifest) Artifact(platform, kind string) (Artifact, bool) {
	for _, a := range m.Artifacts {
		if a.Platform == platform && a.Kind == kind {
			return a, true
		}
	}
	return Artifact{}, false
}

// Describe is an artifact for the manifest: its name, size and checksum taken from body.
func Describe(name, platform, kind string, body []byte) Artifact {
	sum := sha256.Sum256(body)
	return Artifact{Name: name, Platform: platform, Kind: kind, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
}

// ReleaseKeys parses hex ed25519 public keys separated by commas or spaces.
func ReleaseKeys(s string) ([]ed25519.PublicKey, error) {
	var keys []ed25519.PublicKey
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		b, err := hex.DecodeString(f)
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("release key %q is not a hex ed25519 public key", f)
		}
		keys = append(keys, ed25519.PublicKey(b))
	}
	return keys, nil
}

// Newer says whether version a is newer than b. Versions are semver, with or without a leading v; a pre-release
// (1.8.0-nightly.20261015) comes before its release. A version that doesn't parse ("dev") is never newer or older.
func Newer(a, b string) bool {
	av, err := semver.Parse(a)
	if err != nil {
		return false
	}
	bv, err := semver.Parse(b)
	if err != nil {
		return false
	}
	return av.Compare(bv) > 0
}
