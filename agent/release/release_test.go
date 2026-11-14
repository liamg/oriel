package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/internal/isolate"
)

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

// A download that matches its checksum lands whole, with its mode; one that doesn't, or is too big, leaves nothing.
func TestDownloadVerified(t *testing.T) {
	isolate.Test(t)
	body := []byte("#!/bin/sh\necho claude\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	ctx, dir := context.Background(), t.TempDir()
	dst := filepath.Join(dir, "claude")
	if err := DownloadVerified(ctx, srv.Client(), srv.URL, sum(body), dst, 0o755, 1<<20, "Claude Code"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != string(body) {
		t.Errorf("it holds %q", b)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	other := filepath.Join(dir, "other")
	if err := DownloadVerified(ctx, srv.Client(), srv.URL, sum([]byte("else")), other, 0o755, 1<<20, "Claude Code"); err == nil {
		t.Error("a mismatch was kept")
	}
	if err := DownloadVerified(ctx, srv.Client(), srv.URL, sum(body), other, 0o755, 4, "Claude Code"); err == nil {
		t.Error("a download over its bound was kept")
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("left behind: %v", es)
	}
}

// Verified is the body once it checks, and a JSON answer decodes, with the headers asked for.
func TestVerifiedAndGetJSON(t *testing.T) {
	isolate.Test(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meta" {
			w.Write([]byte(`{"accept":"` + r.Header.Get("Accept") + `"}`))
			return
		}
		w.Write([]byte("archive"))
	}))
	defer srv.Close()
	ctx := context.Background()
	if b, err := Verified(ctx, srv.Client(), srv.URL, sum([]byte("archive")), 1<<20, "Codex"); err != nil || string(b) != "archive" {
		t.Errorf("Verified = %q, %v", b, err)
	}
	if _, err := Verified(ctx, srv.Client(), srv.URL, sum([]byte("x")), 1<<20, "Codex"); err == nil {
		t.Error("a mismatch verified")
	}
	var v struct{ Accept string }
	if err := GetJSON(ctx, srv.Client(), srv.URL+"/meta", http.Header{"Accept": {"application/vnd.github+json"}}, &v); err != nil || v.Accept != "application/vnd.github+json" {
		t.Errorf("GetJSON = %+v, %v", v, err)
	}
}
