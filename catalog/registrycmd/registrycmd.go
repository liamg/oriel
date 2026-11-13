// Package registrycmd is `oriel plugin registry`: what the plugin registry repo's CI runs. It's also
// cmd/oriel-registry, which builds with no cgo, so CI needn't build the app.
package registrycmd

import (
	"cmp"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/tools/depcheck"
)

// Usage is the commands' usage.
const Usage = `oriel plugin registry check --base DIR [--head DIR] --author LOGIN [--allow FILE] [--comment FILE]
                                         the registry's PR check: every changed plugin.toml, with the comment it posts
oriel plugin registry index --key FILE [--registry OWNER/NAME] [--out DIR] [DIR]
                                         build the registry's index.json and sign it into index.sig
oriel plugin registry keygen --out FILE  make a registry signing key: the private key into FILE, the public printed`

// Run is `oriel plugin registry …`: what the registry repo's CI runs, checking PRs and building the signed index.
func Run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n%s", Usage)
	}
	fs := flag.NewFlagSet("oriel plugin registry "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, Usage) }
	base := fs.String("base", "", "the registry checkout at the PR's base")
	head := fs.String("head", ".", "the registry checkout at the PR's head")
	author := fs.String("author", "", "the PR's author's GitHub login")
	allow := fs.String("allow", "", "Oriel's tools/depcheck/allowed.txt (none: every dependency is outside it)")
	comment := fs.String("comment", "", "write the comment here as well")
	keyFile := fs.String("key", "", "the registry's private key file; $ORIEL_REGISTRY_KEY when it's -")
	registry := fs.String("registry", catalog.DefaultRepo, "the registry repo, owner/name")
	out := fs.String("out", "", "where index writes index.json and index.sig (the checkout by default); where keygen writes the key")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	ctx := context.Background()
	gh := catalog.GitHub{Token: cmp.Or(os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"))}
	switch args[0] {
	case "check":
		if *base == "" || *author == "" {
			return errors.New("check needs --base and --author")
		}
		c := catalog.PRCheck{Base: *base, Head: *head, Author: *author, Org: gh, Asset: gh.Asset, Allow: depcheck.Allowlist{}}
		if *allow != "" {
			f, err := os.Open(*allow)
			if err != nil {
				return err
			}
			c.Allow, err = depcheck.ParseAllowlist(f)
			f.Close()
			if err != nil {
				return err
			}
		}
		rep, err := c.Run(ctx)
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, rep.Comment)
		if *comment != "" {
			if err := os.WriteFile(*comment, []byte(rep.Comment), 0o644); err != nil {
				return err
			}
		}
		if len(rep.Problems) > 0 {
			return fmt.Errorf("%d %s", len(rep.Problems), plural(len(rep.Problems), "problem", "problems"))
		}
		return nil
	case "index":
		key, err := readRegistryKey(*keyFile)
		if err != nil {
			return err
		}
		dir := "."
		if fs.NArg() > 0 {
			dir = fs.Arg(0)
		}
		ix, err := catalog.BuildIndex(dir, *registry, time.Now())
		if err != nil {
			return err
		}
		body, sig, err := catalog.SignIndex(key, ix)
		if err != nil {
			return err
		}
		to := cmp.Or(*out, dir)
		if err := os.MkdirAll(to, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, catalog.IndexFile), body, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(to, catalog.IndexSig), sig, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%d plugins, signed by %s\n", len(ix.Plugins), catalog.FormatKey(key.Public().(ed25519.PublicKey)))
		return nil
	case "keygen":
		if *out == "" {
			return errors.New("keygen needs --out FILE for the private key")
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		fmt.Fprintln(f, hex.EncodeToString(priv.Seed()))
		if err := f.Close(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, catalog.FormatKey(pub))
		return nil
	}
	return fmt.Errorf("unknown command %q\nusage:\n%s", args[0], Usage)
}

// readRegistryKey reads a private key as keygen writes it: the seed in hex. "-" reads $ORIEL_REGISTRY_KEY.
func readRegistryKey(file string) (ed25519.PrivateKey, error) {
	var s string
	switch file {
	case "":
		return nil, errors.New("index needs --key")
	case "-":
		s = os.Getenv("ORIEL_REGISTRY_KEY")
	default:
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		s = string(b)
	}
	seed, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errors.New("the registry key isn't an ed25519 seed in hex, as keygen writes it")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
