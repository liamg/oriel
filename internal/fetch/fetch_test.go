package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/internal/isolate"
)

// A download that matches its checksum lands whole, with its mode; one that doesn't, or is too big, leaves nothing.
func TestDownload(t *testing.T) {
	isolate.Test(t)
	body := []byte("#!/bin/sh\necho claude\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	ctx, dir := context.Background(), t.TempDir()
	dst := filepath.Join(dir, "claude")
	if err := Download(ctx, srv.Client(), srv.URL, Sum(body), dst, 0o755, 1<<20); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != string(body) {
		t.Errorf("it holds %q", b)
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	other := filepath.Join(dir, "other")
	if err := Download(ctx, srv.Client(), srv.URL, Sum([]byte("else")), other, 0o755, 1<<20); !errors.Is(err, ErrMismatch) {
		t.Errorf("a mismatch: %v", err)
	}
	if err := Download(ctx, srv.Client(), srv.URL, Sum(body), other, 0o755, 4); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a download over its bound: %v", err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("left behind: %v", es)
	}
}

// Verified is the body once it checks; the sum may be upper case or carry its "sha256:" prefix.
func TestVerified(t *testing.T) {
	isolate.Test(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("archive")) }))
	defer srv.Close()
	ctx := context.Background()
	for _, want := range []string{Sum([]byte("archive")), strings.ToUpper(Sum([]byte("archive"))), "sha256:" + Sum([]byte("archive"))} {
		if b, err := Verified(ctx, srv.Client(), srv.URL, want, 1<<20); err != nil || string(b) != "archive" {
			t.Errorf("Verified(%s) = %q, %v", want, b, err)
		}
	}
	if _, err := Verified(ctx, srv.Client(), srv.URL, Sum([]byte("x")), 1<<20); !errors.Is(err, ErrMismatch) {
		t.Errorf("a mismatch: %v", err)
	}
	if _, err := Verified(ctx, srv.Client(), srv.URL, "", 1<<20); !errors.Is(err, ErrMismatch) {
		t.Errorf("no sum to check against: %v", err)
	}
}
