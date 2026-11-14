package kit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/render/svg"
	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/ui"
)

// Pictures are a window's images decoded from files, data: URIs and the web, each read and decoded off the loop
// through a cache: a file's is kept while it stays the same, a remote one's until it's fetched again. Call its
// methods on the loop.
type Pictures struct {
	q      *landing
	files  *plugin.Cache[string, *Picture]
	data   *plugin.Cache[string, *Picture]
	remote *plugin.Cache[string, *Picture]
	made   map[string]made
	note   func(ui.LoadState) // told each read from disk's state
	// remoteOff is markdown.remote_images turned off: no remote image is fetched.
	remoteOff atomic.Bool
	client    *http.Client // what remote images are fetched with
	mu        sync.Mutex
	retries   map[string]retry // failed remote fetches, by URL
	// Clock is the clock a failed fetch's retry is timed by; the system's if nil.
	Clock clock.Clock
}

type made struct {
	from any
	pc   *Picture
}

// retry is a remote image's failed fetches in a row, and when it's fetched again.
type retry struct {
	tries int
	at    time.Time
}

// NewPictures fetches remote images with client, public addresses only if nil, and calls wake as each comes in.
func NewPictures(client *http.Client, wake func()) *Pictures {
	if wake == nil {
		wake = func() {}
	}
	return newPictures(&landing{wake: wake}, ReadsOpts{Client: client})
}

func newPictures(q *landing, o ReadsOpts) *Pictures {
	client := o.Client
	if client == nil {
		client = newPictureClient(egress.DialControl(egress.Public))
	}
	frame := o.Frame
	if frame == nil {
		frame = func() int { return 0 }
	}
	p := &Pictures{q: q, client: client, made: map[string]made{}, retries: map[string]retry{}, note: func(ui.LoadState) {}}
	size := func(pc *Picture) int64 { return pc.Bytes }
	p.files = plugin.NewCache(q, func(_ context.Context, path string) (*Picture, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return decoded(b)
	}, plugin.CacheOpts[string, *Picture]{MaxEntries: maxPictures, MaxBytes: maxPictureBytes, Size: size, Frame: frame, Clock: o.Clock,
		Watches: o.Watches, Path: func(p string) string { return p }})
	p.data = plugin.NewCache(q, func(_ context.Context, uri string) (*Picture, error) { return dataPicture(uri) },
		plugin.CacheOpts[string, *Picture]{MaxEntries: maxPictures, MaxBytes: maxPictureBytes, Size: size, Frame: frame, Clock: o.Clock})
	p.remote = plugin.NewCache(q, p.fetch, plugin.CacheOpts[string, *Picture]{MaxEntries: maxPictures, MaxBytes: maxPictureBytes,
		Size: size, Frame: frame, Clock: o.Clock, Timeout: remoteTimeout})
	return p
}

// Close stops the fetches.
func (p *Pictures) Close() {
	p.files.Close()
	p.data.Close()
	p.remote.Close()
	p.client.CloseIdleConnections()
}

// PicturesOf are the window's pictures, as c's UI was given them; its own for a UI that wasn't.
func PicturesOf(c *ui.Ctx) *Pictures {
	return ui.Keep(c, func() *Pictures { return ReadsIn(c).Pictures })
}

// SetRemote fetches remote images, or (off) answers each with an error saying markdown.remote_images is off.
func (p *Pictures) SetRemote(on bool) { p.remoteOff.Store(!on) }

// Memo is the picture make makes from from, kept under key until from changes: an icon tinted from its image.
func (p *Pictures) Memo(key string, from any, make func() *Picture) *Picture {
	if m, ok := p.made[key]; ok && m.from == from {
		return m.pc
	}
	pc := make()
	if len(p.made) >= maxPictures {
		clear(p.made)
	}
	p.made[key] = made{from: from, pc: pc}
	return pc
}

// Picture is a decoded image or an SVG's drawing and its format ("png", "svg"…). One still loading has neither; a
// remote one that failed says when it's fetched again.
type Picture struct {
	Img     image.Image
	Doc     *svg.Doc
	Format  string
	Density float32 // its pixels per point, as its file says: 2 for a Retina screenshot; 0 when it doesn't say
	Err     error
	Loading bool
	Src     string    // a remote one's URL
	Bytes   int64     // the file's size
	Retry   time.Time // when a failed remote one is fetched again
}

const (
	maxPictures     = 128
	maxPictureBytes = 512 << 20
)

// File is the picture in the file at path: an image, or an SVG's drawing.
func (p *Pictures) File(path string) *Picture {
	p.q.run()
	return p.picture(p.files.Get(path))
}

// FromDataURI is the picture a data: URI holds, decoded once.
func (p *Pictures) FromDataURI(uri string) *Picture {
	p.q.run()
	return p.picture(p.data.Get(uri))
}

// picture is a cache's answer as a Picture: loading until its value is in, or why it failed.
func (p *Pictures) picture(pc *Picture, st ui.LoadState) *Picture {
	p.note(st)
	switch {
	case pc != nil:
		return pc
	case st.Phase == ui.Failed:
		return &Picture{Err: st.Err}
	}
	return &Picture{Loading: true}
}

func dataPicture(uri string) (*Picture, error) {
	head, body, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !ok {
		return nil, errors.New("a data: URI with no data")
	}
	var b []byte
	var err error
	if strings.HasSuffix(head, ";base64") {
		b, err = base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	} else {
		var s string
		s, err = url.PathUnescape(body)
		b = []byte(s)
	}
	if err != nil {
		return nil, err
	}
	return decoded(b)
}

// decoded is b read as an SVG when it's markup, else as a raster image.
func decoded(b []byte) (*Picture, error) {
	pc := &Picture{Bytes: int64(len(b))}
	if looksSVG(b) {
		pc.Format = "svg"
		pc.Doc, pc.Err = svg.Parse(b)
	} else {
		pc.Img, pc.Format, pc.Err = DecodeImage(b)
		if pc.Format == "png" {
			pc.Density = pngDensity(b)
		}
	}
	if pc.Err != nil {
		return nil, pc.Err
	}
	return pc, nil
}

const maxRedirects = 3

// newPictureClient is a client for remote images: http(s) only, at most maxRedirects redirects, and control checks
// the address of every connection it dials, each redirect's too.
func newPictureClient(control func(network, address string, c syscall.RawConn) error) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: control}
	return &http.Client{
		Transport: &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("more than %d redirects", maxRedirects)
			}
			return httpOnly(req.URL)
		},
	}
}

func httpOnly(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s: not an http(s) URL", u.Redacted())
	}
	return nil
}

func getPicture(ctx context.Context, client *http.Client, src string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	if err := httpOnly(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxRemote+1))
	if err == nil && len(b) > maxRemote {
		err = errors.New("the image is over 20 MB")
	}
	return b, err
}

const (
	maxRemote     = 20 << 20
	remoteTimeout = 30 * time.Second
	// A failed fetch is tried again after retryFirst, doubling each time up to retryMax.
	retryFirst = 30 * time.Second
	retryMax   = 10 * time.Minute
)

// Remote is the image at an http(s) URL, fetched in the background: loading until it's in. A failed fetch is tried
// again once its backoff has passed.
func (p *Pictures) Remote(src string) *Picture {
	if p.remoteOff.Load() {
		return &Picture{Err: errors.New("remote images are turned off (markdown.remote_images)")}
	}
	p.q.run()
	pc, st := p.remote.Get(src)
	if st.Phase == ui.Failed {
		p.mu.Lock()
		at := p.retries[src].at
		p.mu.Unlock()
		if !clock.Or(p.Clock).Now().Before(at) {
			p.remote.Invalidate(src)
			pc, st = p.remote.Get(src)
		} else {
			return &Picture{Err: st.Err, Src: src, Retry: at}
		}
	}
	if pc == nil {
		return &Picture{Loading: true, Src: src}
	}
	return pc
}

func (p *Pictures) fetch(ctx context.Context, src string) (*Picture, error) {
	b, err := getPicture(ctx, p.client, src)
	var pc *Picture
	if err == nil {
		pc, err = decoded(b)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err == nil {
		delete(p.retries, src)
		pc.Src = src
		return pc, nil
	}
	tries := p.retries[src].tries
	p.retries[src] = retry{tries: tries + 1, at: clock.Or(p.Clock).Now().Add(min(retryFirst<<min(tries, 10), retryMax))}
	return nil, err
}

// RetryNow fetches a failed remote image again at its next ask.
func (p *Pictures) RetryNow(src string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r, ok := p.retries[src]; ok {
		r.at = time.Time{}
		p.retries[src] = r
	}
}

// looksSVG reports whether b is markup with an <svg> element near its start, past any XML declaration or doctype.
func looksSVG(b []byte) bool {
	b = bytes.TrimLeft(b, "\ufeff \t\r\n")
	return bytes.HasPrefix(b, []byte("<")) && bytes.Contains(bytes.ToLower(b[:min(len(b), 1024)]), []byte("<svg"))
}
