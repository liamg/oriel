// Command release makes a signed release of what's in a directory, as release.yml publishes it: each
// oriel-<version>-linux-<arch>.tar.gz archive, oriel-<goos>-<goarch> binary and oriel-<goos>-<goarch>.app.tar.gz
// macOS bundle as an artifact, with manifest.json and manifest.sig (update package). Anything else there (the DMGs,
// another version's archive) isn't the updater's, and isn't listed. The ed25519 key is
// ORIEL_RELEASE_KEY, hex: a 32-byte seed or a 64-byte private key.
//
//	go run ./tools/cmd/release -version v0.5.0 -in dist -out release [-notes notes.md]
//	go run ./tools/cmd/release -genkey    (a new key: the secret's value, and the public half builds trust)
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/update"
)

func main() {
	version := flag.String("version", "", "the release's version tag: v0.5.0, or v0.5.0-rc.1 for nightly")
	in := flag.String("in", "dist", "the directory of Linux archives, oriel-<goos>-<goarch> binaries and .app.tar.gz bundles")
	out := flag.String("out", "release", "where the release is written")
	notes := flag.String("notes", "", "a markdown file of release notes")
	genkey := flag.Bool("genkey", false, "print a new key pair and exit")
	flag.Parse()
	if *genkey {
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "release:", err)
			os.Exit(1)
		}
		fmt.Printf("ORIEL_RELEASE_KEY (secret):      %x\nORIEL_RELEASE_PUBKEYS (variable): %x\n", priv.Seed(), pub)
		return
	}
	key, err := parseKey(os.Getenv("ORIEL_RELEASE_KEY"))
	if err == nil {
		err = run(*version, *in, *out, *notes, key, time.Now().UTC())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(1)
	}
}

func parseKey(s string) (ed25519.PrivateKey, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	switch {
	case err != nil:
		return nil, fmt.Errorf("ORIEL_RELEASE_KEY isn't hex: %w", err)
	case len(b) == ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case len(b) == ed25519.PrivateKeySize:
		return ed25519.PrivateKey(b), nil
	}
	return nil, errors.New("ORIEL_RELEASE_KEY must be a 32-byte seed or a 64-byte private key, in hex")
}

func run(version, in, out, notesFile string, key ed25519.PrivateKey, now time.Time) error {
	if !strings.HasPrefix(version, "v") {
		return fmt.Errorf("version %q isn't a v-tag", version)
	}
	m := update.Manifest{Version: version, Channel: update.Stable, Published: now}
	if strings.Contains(version, "-") {
		m.Channel = update.Nightly
	}
	if notesFile != "" {
		b, err := os.ReadFile(notesFile)
		if err != nil {
			return err
		}
		m.Notes = string(b)
	}
	entries, err := os.ReadDir(in)
	if err != nil {
		return err
	}
	files := map[string][]byte{}
	for _, e := range entries {
		platform, kind, ok := artifactOf(e.Name(), version)
		if !ok || e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(in, e.Name()))
		if err != nil {
			return err
		}
		m.Artifacts = append(m.Artifacts, update.Describe(e.Name(), platform, kind, b))
		files[e.Name()] = b
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("no oriel artifacts for %s in %s", version, in)
	}
	rel, err := update.Sign(key, m)
	if err != nil {
		return err
	}
	return update.Write(out, rel, files)
}

// artifactOf reads oriel-<version>-linux-<arch>.tar.gz (an archive), oriel-<goos>-<goarch> (a binary) or
// oriel-<goos>-<goarch>.app.tar.gz (a bundle). version is the release's tag, whose v the archive's name drops.
func artifactOf(name, version string) (platform, kind string, ok bool) {
	kind = update.KindBinary
	if base, found := strings.CutSuffix(name, ".app.tar.gz"); found {
		name, kind = base, update.KindBundle
	} else if base, found := strings.CutSuffix(name, ".tar.gz"); found {
		arch, ok := strings.CutPrefix(base, "oriel-"+strings.TrimPrefix(version, "v")+"-linux-")
		if !ok || arch == "" || strings.ContainsAny(arch, "-.") {
			return "", "", false
		}
		return "linux/" + arch, update.KindArchive, true
	}
	rest, ok := strings.CutPrefix(name, "oriel-")
	if !ok {
		return "", "", false
	}
	goos, goarch, ok := strings.Cut(rest, "-")
	if !ok || goos == "" || goarch == "" || strings.ContainsAny(goarch, "-.") {
		return "", "", false
	}
	return goos + "/" + goarch, kind, true
}
