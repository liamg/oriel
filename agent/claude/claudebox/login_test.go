package claudebox

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

// waitForToken waits until root's sandbox holds token.
func waitForToken(t *testing.T, root, token string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(Dir(root), credentialsFile))
		if l, ok := parseLogin(b); ok && l.AccessToken() == token {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the sandbox never got %q: %s", token, b)
		}
		time.Sleep(5 * time.Millisecond)
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
	writeLogin(t, host, "first", time.Now().Add(time.Hour))
	box := Box{KeepEvery: 10 * time.Millisecond}
	release := box.KeepLogins(func(context.Context) { t.Error("a login an hour from its end was renewed") })
	defer release()
	waitForToken(t, root, "first")
	writeLogin(t, host, "second", time.Now().Add(8*time.Hour))
	waitForToken(t, root, "second")
}

// A host login near its end is renewed on the host (sandboxes can't), once, and the new one handed on.
func TestKeepLoginsRenewsALoginNearItsEnd(t *testing.T) {
	host, root := keepHome(t)
	writeLogin(t, host, "old", time.Now().Add(time.Minute))
	var renewals atomic.Int32
	box := Box{KeepEvery: 10 * time.Millisecond}
	release := box.KeepLogins(func(context.Context) {
		renewals.Add(1)
		writeLogin(t, host, "renewed", time.Now().Add(8*time.Hour))
	})
	waitForToken(t, root, "renewed")
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
