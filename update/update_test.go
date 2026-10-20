package update_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/update"
)

func signed(t *testing.T, key ed25519.PrivateKey, version string, arts ...update.Artifact) update.Release {
	t.Helper()
	rel, err := update.Sign(key, update.Manifest{Version: version, Artifacts: arts})
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// A release verifies only with a trusted key and only as signed: one changed byte, or another key, and its manifest
// isn't read. Two keys are trusted during a rotation.
//
// ref:   go/src/crypto/ed25519/ed25519.go#Verify
// quote: "Verify reports whether sig is a valid signature of message by publicKey."
func TestReleaseVerify(t *testing.T) {
	key, other := testKey(t), testKey(t)
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	rel := signed(t, key, "1.2.0")

	m, err := rel.Verify(trust)
	if err != nil || m.Version != "1.2.0" {
		t.Fatalf("Verify = %+v, %v; want 1.2.0", m, err)
	}
	tampered := update.Release{Manifest: bytes.Replace(rel.Manifest, []byte("1.2.0"), []byte("9.9.9"), 1), Signature: rel.Signature}
	if _, err := tampered.Verify(trust); !errors.Is(err, update.ErrUnsigned) {
		t.Errorf("a tampered manifest verified: %v", err)
	}
	if _, err := signed(t, other, "1.2.0").Verify(trust); !errors.Is(err, update.ErrUnsigned) {
		t.Errorf("another key's release verified: %v", err)
	}
	if _, err := rel.Verify(nil); err == nil {
		t.Error("a build trusting no key verified a release")
	}
	rotating := append(trust, other.Public().(ed25519.PublicKey))
	if _, err := signed(t, other, "1.3.0").Verify(rotating); err != nil {
		t.Errorf("the new key's release didn't verify during a rotation: %v", err)
	}
}

func TestReleaseKeys(t *testing.T) {
	a, b := testKey(t).Public().(ed25519.PublicKey), testKey(t).Public().(ed25519.PublicKey)
	keys, err := update.ReleaseKeys(hex.EncodeToString(a) + "," + hex.EncodeToString(b))
	if err != nil || len(keys) != 2 || !a.Equal(keys[0]) || !b.Equal(keys[1]) {
		t.Fatalf("ReleaseKeys = %v, %v", keys, err)
	}
	if _, err := update.ReleaseKeys("abcd"); err == nil {
		t.Error("a short key parsed")
	}
	if len(update.Keys()) != 0 {
		t.Error("an unstamped build trusts a key")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"1.8.0", "1.7.9", true},
		{"v1.10.0", "1.9.0", true},
		{"1.8.0", "1.8.0", false},
		{"1.7.0", "1.8.0", false},
		{"1.8.0", "1.8.0-nightly.20261015", true},
		{"1.8.0-nightly.20261016", "1.8.0-nightly.20261015", true},
		{"1.8.0-nightly.20261015", "1.7.0", true},
		{"1.0.0", "dev", false},
	} {
		if got := update.Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func installer(t *testing.T) *update.Installer {
	t.Helper()
	root := t.TempDir()
	in := &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"),
		Probe: func(context.Context, string, string) error { return nil }}
	if err := os.WriteFile(in.Target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	return in
}

func stage(t *testing.T, in *update.Installer, version string, body []byte) {
	t.Helper()
	a := update.Describe("oriel", update.Platform(), update.KindBinary, body)
	if err := in.Stage(context.Background(), update.Manifest{Version: version}, a, bytes.NewReader(body)); err != nil {
		t.Fatal(err)
	}
}

func TestStageRefusesWhatTheManifestDoesNotDescribe(t *testing.T) {
	in := installer(t)
	a := update.Describe("oriel", update.Platform(), update.KindBinary, []byte("new"))
	for name, body := range map[string]string{"changed": "neW", "short": "ne", "long": "newer"} {
		if err := in.Stage(context.Background(), update.Manifest{Version: "2.0.0"}, a, strings.NewReader(body)); err == nil {
			t.Errorf("%s bytes were staged", name)
		}
	}
	if _, ok := in.Staged(); ok {
		t.Error("something is staged")
	}
	if err := in.Swap(); err == nil {
		t.Error("Swap with nothing staged succeeded")
	}
}

// A staged binary that won't run, or reports another version, is refused before anything is swapped.
func TestStageRunsTheNewBinary(t *testing.T) {
	in := installer(t)
	in.Probe = nil
	sh := []byte("#!/bin/sh\necho oriel 1.9.0\n")
	a := update.Describe("oriel", update.Platform(), update.KindBinary, sh)
	err := in.Stage(context.Background(), update.Manifest{Version: "2.0.0"}, a, bytes.NewReader(sh))
	if err == nil || !strings.Contains(err.Error(), "1.9.0") {
		t.Fatalf("a binary reporting 1.9.0 staged as 2.0.0: %v", err)
	}
	sh = []byte("#!/bin/sh\necho oriel 2.0.0\n")
	a = update.Describe("oriel", update.Platform(), update.KindBinary, sh)
	if err := in.Stage(context.Background(), update.Manifest{Version: "2.0.0"}, a, bytes.NewReader(sh)); err != nil {
		t.Fatal(err)
	}
	if v, ok := in.Staged(); !ok || v != "2.0.0" {
		t.Fatalf("Staged = %q, %v", v, ok)
	}
}

// A new version that crashes or hangs before it reports healthy is rolled back on the next start.
func TestBootRollsBackAStartThatNeverReported(t *testing.T) {
	in := installer(t)
	stage(t, in, "2.0.0", []byte("new"))
	if err := in.Swap(); err != nil {
		t.Fatal(err)
	}
	healthy := func(context.Context) error { return nil }
	hang := func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if out, _ := in.Boot(ctx, hang); out.RolledBack == nil {
		t.Fatal("a health check that timed out wasn't rolled back")
	}
	if b, _ := os.ReadFile(in.Target); string(b) != "old" {
		t.Fatalf("Target = %q after the rollback", b)
	}

	stage(t, in, "2.0.1", []byte("newer"))
	if err := in.Swap(); err != nil {
		t.Fatal(err)
	}
	if p, ok := in.Pending(); !ok || p.Version != "2.0.1" {
		t.Fatalf("Pending = %+v, %v", p, ok)
	}
	// The first start marks its attempt and then dies before its check finishes.
	p, _ := in.Pending()
	p.Boots = 1
	b, _ := jsonOf(p)
	if err := os.WriteFile(filepath.Join(in.Dir, "pending.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := in.Boot(context.Background(), healthy)
	if err != nil || out.RolledBack == nil || out.RolledBack.Version != "2.0.1" {
		t.Fatalf("Boot after a crash = %+v, %v; want 2.0.1 rolled back", out, err)
	}
	if b, _ := os.ReadFile(in.Target); string(b) != "old" {
		t.Fatalf("Target = %q after the rollback", b)
	}
	if out, err := in.Boot(context.Background(), healthy); err != nil || out != (update.Outcome{}) {
		t.Fatalf("Boot with nothing pending = %+v, %v", out, err)
	}

	stage(t, in, "2.0.2", []byte("newest"))
	if err := in.Swap(); err != nil {
		t.Fatal(err)
	}
	if out, err := in.Boot(context.Background(), healthy); err != nil || out.Confirmed != "2.0.2" {
		t.Fatalf("Boot = %+v, %v; want 2.0.2 confirmed", out, err)
	}
	if _, ok := in.Last(); ok {
		t.Error("a confirmed swap still reports the last rollback")
	}
}

func TestBundleRefusesPathsOutsideIt(t *testing.T) {
	for name, files := range map[string][]tarFile{
		"dotdot":   {{name: "../evil", body: []byte("x"), mode: 0o644}},
		"absolute": {{name: "/etc/evil", body: []byte("x"), mode: 0o644}},
		"link":     {{name: "Contents/l", linkname: "../../../etc"}},
	} {
		in := installer(t)
		in.Exe = "Contents/MacOS/oriel"
		body := tarGz(t, files)
		a := update.Describe("Oriel.app.tar.gz", update.Platform(), update.KindBundle, body)
		if err := in.Stage(context.Background(), update.Manifest{Version: "2.0.0"}, a, bytes.NewReader(body)); err == nil {
			t.Errorf("%s: a bundle reaching outside itself was staged", name)
		}
	}
}

// The app reads releases from GitHub's releases API; here a fake of it, never GitHub itself.
func TestGitHubLatest(t *testing.T) {
	key := testKey(t)
	body := []byte("binary")
	art := update.Describe("oriel-linux-amd64", "linux/amd64", update.KindBinary, body)
	stable, nightly := signed(t, key, "1.8.0", art), signed(t, key, "1.9.0-nightly.1", art)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		asset := func(rel, name string) string {
			return `{"name":"` + name + `","url":"` + srv.URL + `/assets/` + rel + `/` + name + `"}`
		}
		files := map[string][]byte{
			"/assets/n/manifest.json": nightly.Manifest, "/assets/n/manifest.sig": nightly.Signature,
			"/assets/s/manifest.json": stable.Manifest, "/assets/s/manifest.sig": stable.Signature,
			"/assets/s/oriel-linux-amd64": body,
		}
		switch {
		case r.URL.Path == "/repos/liamg/oriel/releases":
			w.Write([]byte(`[{"draft":true,"assets":[]},` +
				`{"prerelease":true,"assets":[` + asset("n", "manifest.json") + `,` + asset("n", "manifest.sig") + `]},` +
				`{"assets":[` + asset("s", "manifest.json") + `,` + asset("s", "manifest.sig") + `,` + asset("s", "oriel-linux-amd64") + `]}]`))
		case files[r.URL.Path] != nil:
			if r.Header.Get("Accept") != "application/octet-stream" {
				http.Error(w, "want octet-stream", http.StatusBadRequest)
				return
			}
			w.Write(files[r.URL.Path])
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	gh := &update.GitHub{Repo: "liamg/oriel", Base: srv.URL, Token: func(context.Context) (string, error) { return "tok", nil }}
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	ctx := context.Background()

	if _, m, err := update.Check(ctx, gh, trust, update.Nightly, "1.8.0"); err != nil || m.Version != "1.9.0-nightly.1" {
		t.Fatalf("nightly = %+v, %v", m, err)
	}
	_, m, err := update.Check(ctx, gh, trust, update.Stable, "1.7.0")
	if err != nil || m.Version != "1.8.0" {
		t.Fatalf("stable = %+v, %v", m, err)
	}
	if _, _, err := update.Check(ctx, gh, trust, update.Stable, "1.8.0"); !errors.Is(err, update.ErrUpToDate) {
		t.Fatalf("Check on 1.8.0 = %v, want ErrUpToDate", err)
	}

	cache := update.Cache{Dir: t.TempDir(), Source: gh}
	r, err := cache.Open(ctx, m, art)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("downloaded %q", got)
	}
	// A cached copy that went bad is downloaded again.
	if err := os.WriteFile(filepath.Join(cache.Dir, "1.8.0", art.Name), []byte("rotten"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err = cache.Open(ctx, m, art)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = io.ReadAll(r)
	r.Close()
	if !bytes.Equal(got, body) {
		t.Fatalf("the cache served %q", got)
	}
}

// A public repo's releases read with no token, or with one GitHub refuses; a private one says it needs a token.
func TestGitHubPublicNeedsNoToken(t *testing.T) {
	key := testKey(t)
	rel := signed(t, key, "1.8.0", update.Describe("oriel-linux-amd64", "linux/amd64", update.KindBinary, []byte("binary")))
	private := false
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
			return
		}
		if private {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		switch r.URL.Path {
		case "/repos/liamg/oriel/releases":
			w.Write([]byte(`[{"assets":[{"name":"manifest.json","url":"` + srv.URL + `/m"},{"name":"manifest.sig","url":"` + srv.URL + `/s"}]}]`))
		case "/m":
			w.Write(rel.Manifest)
		case "/s":
			w.Write(rel.Signature)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	trust := []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}
	ctx := context.Background()
	for name, tok := range map[string]func(context.Context) (string, error){
		"no token":      nil,
		"empty token":   func(context.Context) (string, error) { return "", nil },
		"expired token": func(context.Context) (string, error) { return "old", nil },
	} {
		gh := &update.GitHub{Repo: "liamg/oriel", Base: srv.URL, Token: tok}
		if _, m, err := update.Check(ctx, gh, trust, update.Stable, "1.7.0"); err != nil || m.Version != "1.8.0" {
			t.Errorf("%s: %+v, %v", name, m, err)
		}
	}
	private = true
	_, _, err := update.Check(ctx, &update.GitHub{Repo: "liamg/oriel", Base: srv.URL}, trust, update.Stable, "1.7.0")
	if err == nil || !strings.Contains(err.Error(), "needs a GitHub token") {
		t.Errorf("private without a token = %v", err)
	}
}
