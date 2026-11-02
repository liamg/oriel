package claude

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// signedIn writes a Claude sign-in into a home of the test's own, its token running out at expires.
func signedIn(t *testing.T, expires time.Time) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	doc := fmt.Sprintf(`{"claudeAiOauth":{"accessToken":"tok-1","refreshToken":"r","expiresAt":%d}}`, expires.UnixMilli())
	if err := os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The plan's windows come from the usage API, asked with the sign-in's token: the API's answer as Claude Code reads it.
func TestUsageFromTheAPI(t *testing.T) {
	signedIn(t, time.Now().Add(time.Hour))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-1" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"five_hour":{"utilization":21.0,"resets_at":"2026-11-02T14:00:00.877911+00:00"},
			"seven_day":{"utilization":4.0,"resets_at":"2026-11-08T13:00:00.877932+00:00"},"seven_day_opus":null,"extra_usage":{"is_enabled":true}}`)
	}))
	defer srv.Close()
	p := &Provider{UsageURL: srv.URL}
	u, err := p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Windows) != 2 || u.Windows[0].Name != "Current session" || u.Windows[0].Used != .21 || u.Windows[1].Used != .04 ||
		u.Windows[0].Resets.IsZero() || u.Windows[0].Length != 5*time.Hour || u.Provider != "claude" {
		t.Errorf("usage = %+v", u)
	}
}

// A token that has run out isn't used: the probe reads the windows, and claude refreshes it as it starts.
func TestUsageAPISkipsAnExpiredToken(t *testing.T) {
	signedIn(t, time.Now().Add(-time.Minute))
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { asked = true }))
	defer srv.Close()
	p := &Provider{}
	if _, ok, _ := p.apiUsage(context.Background(), srv.Client(), srv.URL); ok || asked {
		t.Errorf("an expired token was used: ok %v, asked %v", ok, asked)
	}
}
