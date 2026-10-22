package claudebox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
