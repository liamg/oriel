package registrycmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/catalog"
)

// catalog.md, The index: "On merge, CI flattens every plugin.toml into index.json and signs it into index.sig
// (ed25519)."
//
// ref:   go/src/crypto/ed25519/ed25519.go#Verify
// quote: "Verify reports whether sig is a valid signature of message by publicKey."
func TestKeygenAndIndex(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "key")
	var out bytes.Buffer
	if err := Run([]string{"keygen", "--out", keyFile}, &out, &out); err != nil {
		t.Fatal(err)
	}
	pub, err := catalog.ParseKey(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(keyFile); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the private key file: %v %v", fi, err)
	}
	os.MkdirAll(filepath.Join(dir, "plugins", "liamg", "surround"), 0o755)
	os.WriteFile(filepath.Join(dir, "plugins", "liamg", "surround", "plugin.toml"),
		[]byte("repo = \"github.com/liamg/oriel-surround\"\ntrusted = true\nsince = \"2026-10-13\"\n"), 0o644)
	out.Reset()
	if err := Run([]string{"index", "--key", keyFile, dir}, &out, &out); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, catalog.IndexFile))
	sig, _ := os.ReadFile(filepath.Join(dir, catalog.IndexSig))
	ix, err := catalog.VerifyIndex(body, sig, []catalog.PublicKey{pub})
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := ix.Plugin("liamg/surround"); !ok || !l.Trusted || ix.Registry != catalog.DefaultRepo {
		t.Errorf("index %+v", ix)
	}
	tampered := bytes.Replace(body, []byte(`"trusted": true`), []byte(`"trusted": false`), 1)
	if _, err := catalog.VerifyIndex(tampered, sig, []catalog.PublicKey{pub}); !errors.Is(err, catalog.ErrBadSignature) {
		t.Errorf("a changed index: %v", err)
	}
}
