package gitx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// A clone the remote refuses for want of credentials says so as ErrNoCredentials, so a caller tells it apart without
// reading git's words; any other failure doesn't.
func TestCloneWithoutCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	err := CloneProgress(context.Background(), srv.URL+"/repo.git", filepath.Join(t.TempDir(), "repo"), nil)
	if !errors.Is(err, ErrNoCredentials) {
		t.Errorf("a refused clone: %v", err)
	}
	err = CloneProgress(context.Background(), filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "repo"), nil)
	if err == nil || errors.Is(err, ErrNoCredentials) {
		t.Errorf("a missing repo: %v", err)
	}
}
