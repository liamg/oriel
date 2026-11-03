package voice

import (
	"cmp"
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
	"strings"
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
	"tiny.en":   "921e4cf8686fdd993dcd081a5da5b6c365bfde1162e72b08d75ac75289920b1f",
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

// backgroundRate caps a background download, in bytes a second, so it doesn't crowd out the user's own traffic.
var backgroundRate int64 = 4 << 20

var modelName = regexp.MustCompile(`^(tiny|base|small|medium|large-v[123](-turbo)?)(\.en)?$`)

type download struct {
	got, total atomic.Int64
	done       atomic.Bool
	slow       atomic.Bool  // a background download, held to backgroundRate
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

// Download fetches a named model into Dir at full speed, off the caller's goroutine, resuming what an earlier one
// left. Asking again while one runs returns the same download, sped up if it was in the background.
func (w *Whisper) Download(name string) (DownloadStatus, error) { return w.start(name, false) }

func (w *Whisper) start(name string, background bool) (DownloadStatus, error) {
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
		d.slow.Store(background)
		w.dl[name] = d
		go w.fetch(name, d, modelSource{url: ModelBase + "/ggml-" + name + ".bin", sha: ModelSHA256[name], stall: stallAfter, rate: backgroundRate})
	} else if !background {
		d.slow.Store(false)
	}
	w.dlMu.Unlock()
	w.changed()
	return d.status(name), nil
}

// OnChange calls fn, on a goroutine of the download's, when a download starts, moves by a percent, or ends.
func (w *Whisper) OnChange(fn func()) (remove func()) {
	w.dlMu.Lock()
	defer w.dlMu.Unlock()
	if w.listeners == nil {
		w.listeners = map[int]func(){}
	}
	id := w.nextL
	w.nextL++
	w.listeners[id] = fn
	return func() {
		w.dlMu.Lock()
		defer w.dlMu.Unlock()
		delete(w.listeners, id)
	}
}

func (w *Whisper) changed() {
	w.dlMu.Lock()
	fns := make([]func(), 0, len(w.listeners))
	for _, fn := range w.listeners {
		fns = append(fns, fn)
	}
	w.dlMu.Unlock()
	for _, fn := range fns {
		fn()
	}
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

// Prefetch starts downloading the configured model in the background, held to backgroundRate, when transcription
// can run here and the model is a known name not yet on disk, so the first dictation doesn't wait on it. It doesn't
// start on a network the OS says is metered. It reports whether a download is running.
func (w *Whisper) Prefetch() bool {
	st := w.Settings()
	if st.URL != "" || !w.Installed() {
		return false
	}
	path, name := w.ModelPath(st.Model)
	if _, err := os.Stat(path); err == nil || !modelName.MatchString(name) || ModelSHA256[name] == "" {
		return false
	}
	if d, ok := w.DownloadState(name); ok && !d.Done {
		return true
	}
	if w.Metered != nil && w.Metered() {
		return false
	}
	_, err := w.start(name, true)
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
	rate     int64
}

func (w *Whisper) fetch(name string, d *download, src modelSource) {
	if err := w.fetchTo(name, d, src); err != nil {
		d.err.Store(err.Error())
	}
	d.done.Store(true)
	w.changed()
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
	tmp := dst + ".part"
	h := sha256.New()
	var have int64
	if f, err := os.Open(tmp); err == nil {
		have, _ = io.Copy(h, f)
		_ = f.Close()
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	res, err := cmp.Or(w.Net, modelClient).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	flag := os.O_TRUNC
	switch {
	case res.StatusCode == http.StatusPartialContent && have > 0 && strings.HasPrefix(res.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", have)):
		flag = os.O_APPEND
	case res.StatusCode == http.StatusOK:
		have = 0
		h.Reset()
	default:
		_ = os.Remove(tmp)
		return fmt.Errorf("download failed: %s", res.Status)
	}
	d.got.Store(have)
	if res.ContentLength >= 0 {
		d.total.Store(have + res.ContentLength)
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|flag, 0o644)
	if err != nil {
		return err
	}
	stall := time.AfterFunc(src.stall, func() { cancel(fmt.Errorf("the download stalled for %s", src.stall)) })
	defer stall.Stop()
	c := &counter{r: res.Body, d: d, stall: stall, after: src.stall, rate: src.rate, step: w.changed}
	_, err = io.Copy(io.MultiWriter(f, h), c)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if cause := context.Cause(ctx); err != nil && cause != nil {
		err = cause
	}
	if err != nil {
		return err // the part is kept, and the next download resumes it
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		_ = os.Remove(tmp)
		return fmt.Errorf("the model %s failed its checksum (got sha256 %s, want %s)", name, got, want)
	}
	return os.Rename(tmp, dst)
}

// counter counts what's read into d, holds a slow download to rate, and calls step at each percent.
type counter struct {
	r     io.Reader
	d     *download
	stall *time.Timer
	after time.Duration
	rate  int64
	step  func()
	pct   int64
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	got := c.d.got.Add(int64(n))
	if n > 0 {
		c.stall.Reset(c.after)
		// Held back in short naps, so a download sped up mid-way goes on at once.
		for wait := time.Duration(n) * time.Second / time.Duration(max(c.rate, 1)); wait > 0 && c.d.slow.Load() && c.rate > 0; wait -= 20 * time.Millisecond {
			time.Sleep(min(wait, 20*time.Millisecond))
		}
	}
	if t := c.d.total.Load(); t > 0 && got*100/t != c.pct {
		c.pct = got * 100 / t
		c.step()
	}
	return n, err
}
