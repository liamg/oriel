package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

// credentialsFile is where Claude Code keeps a sign-in under its config dir.
const credentialsFile = ".credentials.json"

// oauthKey is the sign-in's key in the credentials document.
const oauthKey = "claudeAiOauth"

// Login is the host's Claude sign-in, as Claude Code stores it.
type Login map[string]any

// Expiry is when the access token stops working.
func (l Login) Expiry() time.Time {
	ms, _ := l["expiresAt"].(float64)
	return time.UnixMilli(int64(ms))
}

// stripped is the credentials document a sandbox gets: the access token
// without the refresh token, so the sandbox can never rotate the host's.
func (l Login) stripped() ([]byte, error) {
	out := map[string]any{}
	for k, v := range l {
		out[k] = v
	}
	out["refreshToken"] = nil
	delete(out, "refreshTokenExpiresAt")
	return json.MarshalIndent(map[string]any{oauthKey: out}, "", "  ")
}

func parseLogin(b []byte) (Login, bool) {
	var doc map[string]Login
	if json.Unmarshal(b, &doc) != nil {
		return nil, false
	}
	l := doc[oauthKey]
	if tok, _ := l["accessToken"].(string); tok == "" {
		return nil, false
	}
	return l, true
}

var (
	keychainMu    sync.Mutex
	keychainLogin Login
)

// HostLogin reads this machine's Claude sign-in: the credentials file, or the
// macOS Keychain where Claude Code keeps it there.
func HostLogin() (Login, bool) {
	if home, err := os.UserHomeDir(); err == nil {
		if b, err := os.ReadFile(filepath.Join(home, ".claude", credentialsFile)); err == nil {
			if l, ok := parseLogin(b); ok {
				return l, true
			}
		}
	}
	if runtime.GOOS != "darwin" {
		return nil, false
	}
	keychainMu.Lock()
	defer keychainMu.Unlock()
	// The host only rewrites it near expiry, so ask the Keychain again then.
	if keychainLogin != nil && time.Until(keychainLogin.Expiry()) > 10*time.Minute {
		return keychainLogin, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return nil, false
	}
	l, ok := parseLogin(b)
	if ok {
		keychainLogin = l
	}
	return l, ok
}

// SyncLogin gives root's sandbox the host's sign-in. A sign-in made inside the
// sandbox, which has a refresh token of its own, is left alone.
func SyncLogin(root string, l Login) error {
	path := filepath.Join(ClaudeDir(root), credentialsFile)
	cur, err := os.ReadFile(path)
	if err == nil {
		if own, ok := parseLogin(cur); ok {
			if rt, _ := own["refreshToken"].(string); rt != "" {
				return nil
			}
		}
	}
	want, err := l.stripped()
	if err != nil || bytes.Equal(cur, want) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, want, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SyncLogins refreshes the sign-in in every sandbox on this machine.
func SyncLogins(l Login) {
	ents, err := os.ReadDir(Base())
	if err != nil {
		return
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(Base(), e.Name(), "record.json"))
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) == nil && r.Root != "" {
			_ = SyncLogin(r.Root, l)
		}
	}
}
