// Package pricing is the session host's price book: the snapshot vendored with Oriel, or cost.dev's list as last
// fetched, kept under the host's home (prices.json) and fetched again now and then.
package pricing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/internal/atomicfile"
)

// Every is how often the list is fetched again.
const Every = 24 * time.Hour

// retry is how soon a fetch that failed is tried again.
const retry = time.Hour

// timeout bounds one fetch.
const timeout = 30 * time.Second

// maxBody is the most of a response read: the list is a few hundred kilobytes.
const maxBody = 16 << 20

// Deps are what the book needs of the host.
type Deps struct {
	Client *http.Client // http.DefaultClient if nil
	URL    string       // price.URL if ""
}

// Pricing keeps the live book.
type Pricing struct {
	env  hostcore.Env
	deps Deps
	live *price.Live

	mu     sync.Mutex
	etag   string
	tried  time.Time // the last fetch
	failed bool      // and whether it failed
}

// cache is prices.json: the list as cost.dev sent it, with what the next fetch asks with.
type cache struct {
	Fetched time.Time       `json:"fetched"`
	ETag    string          `json:"etag,omitempty"`
	Models  json.RawMessage `json:"models"`
}

// New starts with the snapshot, or the cached list when it's newer.
func New(env hostcore.Env, deps Deps) *Pricing {
	p := &Pricing{env: env, deps: deps, live: price.NewLive(price.Default())}
	c, b, err := p.readCache()
	switch {
	case os.IsNotExist(err):
	case err != nil:
		env.Log.Warn("prices: the cached list", "err", err)
	case b.Generated().After(p.live.Book().Generated()):
		p.live.Set(b)
		p.etag = c.ETag
	}
	return p
}

// Book is the prices now.
func (p *Pricing) Book() *price.Book { return p.live.Book() }

func (p *Pricing) file() string { return filepath.Join(p.env.Home, "prices.json") }

func (p *Pricing) readCache() (cache, *price.Book, error) {
	data, err := os.ReadFile(p.file())
	if err != nil {
		return cache{}, nil, err
	}
	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return cache{}, nil, err
	}
	b, err := price.Parse(c.Models)
	return c, b, err
}

// Run fetches the list now, then whenever a tick finds the last fetch Every old (an hour, after one failed), until
// ctx ends or the host closes.
func (p *Pricing) Run(ctx context.Context, tick <-chan time.Time) {
	p.refresh(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.env.Closed:
			return
		case <-tick:
			if p.due() {
				p.refresh(ctx)
			}
		}
	}
}

func (p *Pricing) due() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	wait := Every
	if p.failed {
		wait = retry
	}
	return !p.env.Now().Before(p.tried.Add(wait))
}

func (p *Pricing) refresh(ctx context.Context) {
	defer p.env.Recover("prices", nil)
	err := p.Refresh(ctx)
	if err != nil {
		p.env.Log.Warn("prices: fetching cost.dev's list", "err", err)
	}
}

// Refresh fetches the list, and swaps it in and caches it if it's good; otherwise the book stays as it was.
func (p *Pricing) Refresh(ctx context.Context) error {
	p.mu.Lock()
	etag := p.etag
	p.tried = p.env.Now()
	p.mu.Unlock()
	data, tag, err := p.fetch(ctx, etag)
	if err == nil && data != nil {
		var b *price.Book
		if b, err = price.Parse(data); err == nil {
			p.live.Set(b)
			err = p.writeCache(cache{Fetched: p.env.Now().UTC(), ETag: tag, Models: data})
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = err != nil
	if err == nil && data != nil {
		p.etag = tag
	}
	return err
}

// fetch is the list, or nil when it hasn't changed since etag.
func (p *Pricing) fetch(ctx context.Context, etag string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := price.URL
	if p.deps.URL != "" {
		url = p.deps.URL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	client := http.DefaultClient
	if p.deps.Client != nil {
		client = p.deps.Client
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNotModified:
		return nil, etag, nil
	case http.StatusOK:
	default:
		return nil, "", fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return data, resp.Header.Get("ETag"), err
}

// writeCache writes prices.json, atomically.
func (p *Pricing) writeCache(c cache) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.env.Home, 0o755); err != nil {
		return err
	}
	return atomicfile.Write(p.file(), data, 0o644)
}
