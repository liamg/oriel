package voice

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func modelServer(t *testing.T, body []byte, hash string) *Whisper {
	t.Helper()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(src.Close)
	oldBase, oldHash := ModelBase, ModelSHA256
	ModelBase, ModelSHA256 = src.URL, map[string]string{"base.en": hash}
	t.Cleanup(func() { ModelBase, ModelSHA256 = oldBase, oldHash })
	return &Whisper{Dir: t.TempDir()}
}

func waitDownload(t *testing.T, w *Whisper) DownloadStatus {
	t.Helper()
	if _, err := w.Download("base.en"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if st, _ := w.DownloadState("base.en"); st.Done {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatal("download never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestDownloadChecksMatch(t *testing.T) {
	model := []byte("ggml model bytes")
	w := modelServer(t, model, sum(model))
	if st := waitDownload(t, w); st.Error != "" {
		t.Fatal(st.Error)
	}
	got, err := os.ReadFile(filepath.Join(w.Dir, "ggml-base.en.bin"))
	if err != nil || !bytes.Equal(got, model) {
		t.Fatalf("model %q %v", got, err)
	}
}

func TestDownloadRefusesAMismatch(t *testing.T) {
	w := modelServer(t, []byte("tampered"), sum([]byte("ggml model bytes")))
	st := waitDownload(t, w)
	if !strings.Contains(st.Error, "checksum") {
		t.Fatalf("error %q", st.Error)
	}
	left, _ := os.ReadDir(w.Dir)
	if len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}

func TestDownloadNeedsAChecksum(t *testing.T) {
	w := modelServer(t, []byte("x"), "")
	if _, err := w.Download("base.en"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err %v", err)
	}
	if _, ok := w.DownloadState("base.en"); ok {
		t.Fatal("a download started")
	}
}

func TestDownloadStalls(t *testing.T) {
	release := make(chan struct{})
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("some"))
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer src.Close()
	defer close(release)
	oldBase, oldHash, oldStall := ModelBase, ModelSHA256, stallAfter
	ModelBase, ModelSHA256, stallAfter = src.URL, map[string]string{"base.en": "ab"}, 50*time.Millisecond
	defer func() { ModelBase, ModelSHA256, stallAfter = oldBase, oldHash, oldStall }()
	w := &Whisper{Dir: t.TempDir()}
	st := waitDownload(t, w)
	if !strings.Contains(st.Error, "stalled") {
		t.Fatalf("error %q", st.Error)
	}
	if left, _ := os.ReadDir(w.Dir); len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
}
