package claudebox

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/sandbox"
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

// AccessToken is the sign-in's OAuth access token.
func (l Login) AccessToken() string {
	tok, _ := l["accessToken"].(string)
	return tok
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

// Box is what Claude's sandboxes on this machine share: the Keychain's sign-in as last read, one download at a
// time, and the keeper of their sign-ins (KeepLogins). A Provider owns one.
type Box struct {
	// Keychain reads Claude Code's sign-in from the Keychain; nil runs macOS's security.
	Keychain func(ctx context.Context) ([]byte, error)
	// KeepEvery is how often KeepLogins hands the sandboxes the host's sign-in; 0 is a minute.
	KeepEvery time.Duration
	// Clock times KeepLogins and tells how near its end a sign-in is; nil is the system's.
	Clock clock.Clock
	// HTTP downloads Claude Code for a sandbox; http.DefaultClient if nil.
	HTTP *http.Client

	keychainMu    sync.Mutex
	keychainLogin Login
	downloads     sync.Mutex

	keepMu  sync.Mutex
	keepers int
	keeping *keeper

	seen      seenCache // what each Claude Code binary has sandboxes told they've seen
	refreshed time.Time
}

// HostLogin reads this machine's Claude sign-in: the credentials file's, or the macOS Keychain's where Claude Code
// keeps it there; with both, the newer, as a file left behind holds a token rotated away since.
func (x *Box) HostLogin() (Login, bool) { return x.hostLogin(false) }

// FreshHostLogin is HostLogin read again from the Keychain, for when it may have just changed.
func (x *Box) FreshHostLogin() (Login, bool) { return x.hostLogin(true) }

func (x *Box) hostLogin(fresh bool) (Login, bool) {
	file, inFile := fileLogin()
	key, inKeychain := x.keychain(fresh)
	switch {
	case inFile && inKeychain:
		if key.Expiry().After(file.Expiry()) {
			return key, true
		}
		return file, true
	case inKeychain:
		return key, true
	}
	return file, inFile
}

// fileLogin is the sign-in in the host's ~/.claude credentials file.
func fileLogin() (Login, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, false
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", credentialsFile))
	if err != nil {
		return nil, false
	}
	return parseLogin(b)
}

// keychain is the sign-in in the macOS Keychain, as last read unless fresh.
func (x *Box) keychain(fresh bool) (Login, bool) {
	if x.Keychain == nil && runtime.GOOS != "darwin" {
		return nil, false
	}
	x.keychainMu.Lock()
	defer x.keychainMu.Unlock()
	// The host only rewrites it near expiry, so ask the Keychain again then.
	if !fresh && x.keychainLogin != nil && time.Until(x.keychainLogin.Expiry()) > 10*time.Minute {
		return x.keychainLogin, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := x.Keychain
	if read == nil {
		read = func(ctx context.Context) ([]byte, error) {
			return exec.CommandContext(ctx, "security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
		}
	}
	b, err := read(ctx)
	if err != nil {
		return nil, false
	}
	l, ok := parseLogin(b)
	if ok {
		x.keychainLogin = l
	}
	return l, ok
}

// refreshBefore is how near its end the host's sign-in is renewed: claude renews one this close as it starts.
const refreshBefore = 5 * time.Minute

// refreshGap is the least time between two renewals, so one that fails isn't tried again every tick.
const refreshGap = 2 * time.Minute

// KeepLogins keeps every sandbox on this machine signed in while it's held: each KeepEvery it hands them the host's
// sign-in, and when that is near its end it runs refresh first (the host's own claude, which renews it). Sandboxes
// have no refresh token, so nothing else renews it. The last release stops it.
func (x *Box) KeepLogins(refresh func(context.Context)) (release func()) {
	x.keepMu.Lock()
	x.keepers++
	if x.keepers == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		x.keeping = &keeper{ctx: ctx, cancel: cancel}
		x.keepAfter(x.keeping, 0, refresh)
	}
	x.keepMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			x.keepMu.Lock()
			x.keepers--
			last, k := x.keepers == 0, x.keeping
			if last {
				x.keeping = nil
				k.cancel()
				if k.timer.Stop() {
					k.runs.Done()
				}
			}
			x.keepMu.Unlock()
			if last {
				k.runs.Wait()
			}
		})
	}
}

// keeper is one stretch of KeepLogins, from the first hold to the last release.
type keeper struct {
	ctx    context.Context
	cancel context.CancelFunc
	timer  clock.Timer
	runs   sync.WaitGroup // the round waiting or running
}

// keepAfter runs a round of keeping after d, and schedules the next when it's done. keepMu is held.
func (x *Box) keepAfter(k *keeper, d time.Duration, refresh func(context.Context)) {
	k.runs.Add(1)
	k.timer = clock.Or(x.Clock).AfterFunc(d, func() {
		defer k.runs.Done()
		if k.ctx.Err() != nil {
			return
		}
		x.keepOnce(k.ctx, refresh)
		x.keepMu.Lock()
		defer x.keepMu.Unlock()
		if k.ctx.Err() == nil {
			x.keepAfter(k, cmp.Or(x.KeepEvery, time.Minute), refresh)
		}
	})
}

// keepOnce hands every sandbox the host's sign-in, renewed first when it's near its end.
func (x *Box) keepOnce(ctx context.Context, refresh func(context.Context)) {
	if ownAuth(agent.SandboxPlace{Env: os.Getenv}, Settings()) {
		return
	}
	l, ok := x.HostLogin()
	if !ok {
		return
	}
	x.keepMu.Lock()
	now := clock.Or(x.Clock).Now()
	renew := l.Expiry().Sub(now) < refreshBefore && now.Sub(x.refreshed) >= refreshGap
	if renew {
		x.refreshed = now
	}
	x.keepMu.Unlock()
	if renew {
		refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		if l, ok = x.FreshHostLogin(); !ok {
			return
		}
	}
	SyncLogins(l)
}

// SyncLogin gives root's sandbox the host's sign-in. A sign-in made inside the
// sandbox, which has a refresh token of its own, is left alone.
func SyncLogin(root string, l Login) error {
	path := filepath.Join(Dir(root), credentialsFile)
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
	return atomicfile.WriteSecret(path, want)
}

// SyncLogins refreshes the sign-in in every sandbox on this machine.
func SyncLogins(l Login) {
	for _, root := range sandbox.Roots() {
		_ = SyncLogin(root, l)
	}
}
