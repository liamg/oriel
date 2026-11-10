package claudebox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/sandbox"
)

func writeCreds(t *testing.T, path, refresh string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{oauthKey: map[string]any{
		"accessToken": "access", "refreshToken": refresh, "expiresAt": 1790000000000,
		"refreshTokenExpiresAt": 1800000000000, "scopes": []string{"user:inference"},
	}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSyncLoginStripsRefreshToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCreds(t, filepath.Join(home, ".claude", credentialsFile), "refresh")
	root := filepath.Join(home, "ws")
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}

	var box Box
	l, ok := box.HostLogin()
	if !ok {
		t.Fatal("no host login")
	}
	if got := l.Expiry().UnixMilli(); got != 1790000000000 {
		t.Fatalf("expiry = %d", got)
	}
	SyncLogins(l)

	path := filepath.Join(Dir(root), credentialsFile)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	got := doc[oauthKey]
	if got["accessToken"] != "access" {
		t.Errorf("accessToken = %v", got["accessToken"])
	}
	if v, has := got["refreshToken"]; !has || v != nil {
		t.Errorf("refreshToken = %v, want null", v)
	}
	if _, has := got["refreshTokenExpiresAt"]; has {
		t.Error("refreshTokenExpiresAt was copied")
	}
}

func TestSyncLoginKeepsSandboxOwnLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, "ws")
	path := filepath.Join(Dir(root), credentialsFile)
	writeCreds(t, path, "its-own")
	before, _ := os.ReadFile(path)

	if err := SyncLogin(root, Login{"accessToken": "host"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("a sandbox's own login was overwritten")
	}
}

func writeLogin(t *testing.T, path, token string, expiry time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{oauthKey: map[string]any{
		"accessToken": token, "refreshToken": "refresh", "expiresAt": expiry.UnixMilli()}})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// wantToken checks root's sandbox holds token.
func wantToken(t *testing.T, root, token string) {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(Dir(root), credentialsFile))
	if l, ok := parseLogin(b); !ok || l.AccessToken() != token {
		t.Fatalf("the sandbox has %s, want %q", b, token)
	}
}

func keepHome(t *testing.T) (host, root string) {
	t.Helper()
	isolate.Test(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	root = filepath.Join(home, "ws")
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".claude", credentialsFile), root
}

// While a sandbox's session runs, the host's login reaches it each tick: a login the host renews later too.
func TestKeepLoginsHandsSandboxesTheHostsLogin(t *testing.T) {
	host, root := keepHome(t)
	clk := clocktest.New(time.Now())
	writeLogin(t, host, "first", clk.Now().Add(time.Hour))
	box := Box{KeepEvery: time.Minute, Clock: clk}
	release := box.KeepLogins(func(context.Context) { t.Error("a login an hour from its end was renewed") })
	defer release()
	clk.Advance(0)
	wantToken(t, root, "first")
	writeLogin(t, host, "second", clk.Now().Add(8*time.Hour))
	clk.Advance(time.Minute - time.Nanosecond)
	wantToken(t, root, "first")
	clk.Advance(time.Nanosecond)
	wantToken(t, root, "second")
}

// A host login near its end is renewed on the host (sandboxes can't), once, and the new one handed on.
func TestKeepLoginsRenewsALoginNearItsEnd(t *testing.T) {
	host, root := keepHome(t)
	clk := clocktest.New(time.Now())
	writeLogin(t, host, "old", clk.Now().Add(time.Minute))
	var renewals atomic.Int32
	box := Box{KeepEvery: time.Minute, Clock: clk}
	release := box.KeepLogins(func(context.Context) {
		renewals.Add(1)
		writeLogin(t, host, "renewed", clk.Now().Add(8*time.Hour))
	})
	clk.Advance(0)
	wantToken(t, root, "renewed")
	clk.Advance(3 * time.Minute)
	release()
	if n := renewals.Load(); n != 1 {
		t.Errorf("renewed %d times", n)
	}
}

// The Keychain is asked again for a fresh read, so a login made on the host since shows at once.
func TestFreshHostLoginAsksTheKeychainAgain(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tok := "first"
	box := Box{Keychain: func(context.Context) ([]byte, error) {
		return json.Marshal(map[string]any{oauthKey: map[string]any{"accessToken": tok,
			"expiresAt": time.Now().Add(time.Hour).UnixMilli()}})
	}}
	if l, _ := box.HostLogin(); l.AccessToken() != "first" {
		t.Fatalf("HostLogin = %v", l)
	}
	tok = "second"
	if l, _ := box.HostLogin(); l.AccessToken() != "first" {
		t.Errorf("a cached read = %v", l)
	}
	if l, _ := box.FreshHostLogin(); l.AccessToken() != "second" {
		t.Errorf("FreshHostLogin = %v", l)
	}
}

// A credentials file left behind beside the Keychain's newer sign-in (Claude Code keeps it in the Keychain on macOS)
// isn't handed to the sandboxes: its token was rotated away long since, and the API answers it as revoked. The
// newer of the two is the host's sign-in.
func TestHostLoginIsTheNewerOfTheFileAndTheKeychain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sign := func(tok string, until time.Time) []byte {
		b, _ := json.Marshal(map[string]any{oauthKey: map[string]any{"accessToken": tok, "expiresAt": until.UnixMilli()}})
		return b
	}
	file := filepath.Join(home, ".claude", credentialsFile)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, sign("left behind", time.Now().Add(-30*24*time.Hour)), 0o600); err != nil {
		t.Fatal(err)
	}
	box := Box{Keychain: func(context.Context) ([]byte, error) { return sign("keychain", time.Now().Add(time.Hour)), nil }}
	if l, _ := box.FreshHostLogin(); l.AccessToken() != "keychain" {
		t.Errorf("with a stale file beside the Keychain, HostLogin = %q, want the Keychain's", l.AccessToken())
	}
	if err := os.WriteFile(file, sign("file", time.Now().Add(2*time.Hour)), 0o600); err != nil {
		t.Fatal(err)
	}
	if l, _ := box.FreshHostLogin(); l.AccessToken() != "file" {
		t.Errorf("with the file the newer, HostLogin = %q, want the file's", l.AccessToken())
	}
}
