package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync/atomic"
	"time"
)

// ModelRevision is the commit of huggingface.co/ggerganov/whisper.cpp models
// are fetched from.
const ModelRevision = "5359861c739e955e79d9a303bcbc70fb988958b1"

// ModelBase is where models are fetched from; swapped in tests.
var ModelBase = "https://huggingface.co/ggerganov/whisper.cpp/resolve/" + ModelRevision

// ModelSHA256 is each model file's checksum at ModelRevision. A model with no
// checksum is not downloaded.
var ModelSHA256 = map[string]string{
	"base.en":   "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002",
	"small.en":  "c6138d6d58ecc8322097e0f987c32f1be8bb0a18532a3f88f734d1bbf9c41e5d",
	"medium.en": "cc37e93478338ec7700281a7ac30a10128929eb8f427dda2e865faa8f6da4356",
}

// modelClient fetches models: bounded connect and header waits, and no overall
// timeout, since a model is hundreds of MB.
var modelClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: time.Minute,
	IdleConnTimeout:       90 * time.Second,
}}

// stallAfter ends a download that has received nothing for this long.
var stallAfter = time.Minute

var modelName = regexp.MustCompile(`^(tiny|base|small|medium|large-v[123](-turbo)?)(\.en)?$`)

type download struct {
	got, total atomic.Int64
	done       atomic.Bool
	err        atomic.Value // string
}

// DownloadStatus is one model download's progress.
type DownloadStatus struct {
	Model string `json:"model"`
	Got   int64  `json:"got"`
	Total int64  `json:"total"`
	Done  bool   `json:"done"`
	Error string `json:"error,omitempty"`
}

// Download fetches a named model into Dir in the background. Asking again
// while one runs returns the same download.
func (w *Whisper) Download(name string) (DownloadStatus, error) {
	if !modelName.MatchString(name) {
		return DownloadStatus{}, fmt.Errorf("unknown model %q", name)
	}
	if ModelSHA256[name] == "" {
		return DownloadStatus{}, fmt.Errorf("oriel has no checksum for the model %s, so it will not download it; put ggml-%s.bin in %s yourself", name, name, w.Dir)
	}
	w.dlMu.Lock()
	if w.dl == nil {
		w.dl = map[string]*download{}
	}
	d := w.dl[name]
	if d == nil || (d.done.Load() && d.err.Load() != nil) {
		d = &download{}
		w.dl[name] = d
		go w.fetch(name, d, modelSource{url: ModelBase + "/ggml-" + name + ".bin", sha: ModelSHA256[name], stall: stallAfter})
	}
	w.dlMu.Unlock()
	return d.status(name), nil
}

// DownloadingError is a transcription asked for while its model is still
// arriving.
type DownloadingError struct{ Status DownloadStatus }

func (e *DownloadingError) Error() string {
	if e.Status.Total > 0 {
		return fmt.Sprintf("the voice model %s is still downloading (%d%%)", e.Status.Model, e.Status.Got*100/e.Status.Total)
	}
	return "the voice model " + e.Status.Model + " is still downloading"
}

// Prefetch starts downloading the configured model when whisper-server is
// installed and the model is a known name not yet on disk, so the first
// dictation does not wait on it. It reports whether a download is running.
func (w *Whisper) Prefetch() bool {
	st := w.Settings()
	if st.URL != "" {
		return false
	}
	if _, err := w.LookPath("whisper-server"); err != nil {
		return false
	}
	path, name := w.ModelPath(st.Model)
	if _, err := os.Stat(path); err == nil || !modelName.MatchString(name) {
		return false
	}
	_, err := w.Download(name)
	return err == nil
}

// DownloadState is the latest download of name, if any.
func (w *Whisper) DownloadState(name string) (DownloadStatus, bool) {
	w.dlMu.Lock()
	defer w.dlMu.Unlock()
	d := w.dl[name]
	if d == nil {
		return DownloadStatus{}, false
	}
	return d.status(name), true
}

func (d *download) status(name string) DownloadStatus {
	st := DownloadStatus{Model: name, Got: d.got.Load(), Total: d.total.Load(), Done: d.done.Load()}
	if e, ok := d.err.Load().(string); ok {
		st.Error = e
	}
	return st
}

// modelSource is where one download comes from, read from the package
// settings when it starts.
type modelSource struct {
	url, sha string
	stall    time.Duration
}

func (w *Whisper) fetch(name string, d *download, src modelSource) {
	defer d.done.Store(true)
	if err := w.fetchTo(name, d, src); err != nil {
		d.err.Store(err.Error())
	}
}

func (w *Whisper) fetchTo(name string, d *download, src modelSource) error {
	want := src.sha
	if want == "" {
		return fmt.Errorf("no checksum for the model %s", name)
	}
	dst, _ := w.ModelPath(name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return err
	}
	res, err := modelClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: %s", res.Status)
	}
	d.total.Store(res.ContentLength)
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	stall := time.AfterFunc(src.stall, func() { cancel(fmt.Errorf("the download stalled for %s", src.stall)) })
	defer stall.Stop()
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), &counter{r: res.Body, n: &d.got, stall: stall, after: src.stall})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if cause := context.Cause(ctx); err != nil && cause != nil {
		err = cause
	}
	if err == nil {
		if got := hex.EncodeToString(h.Sum(nil)); got != want {
			err = fmt.Errorf("the model %s failed its checksum (got sha256 %s, want %s)", name, got, want)
		}
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

type counter struct {
	r     io.Reader
	n     *atomic.Int64
	stall *time.Timer
	after time.Duration
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	if n > 0 {
		c.stall.Reset(c.after)
	}
	return n, err
}
