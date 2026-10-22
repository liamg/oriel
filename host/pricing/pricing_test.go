package pricing

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/host/hosttest"
)

// newer is a list built after the snapshot, in cost.dev's shape, pricing one model.
//
// ref:   cost-dev/models.json
// quote: {"data_version":{"git_commit":"e7025b9c0ab66f589f342d1625d657346b7cf0e8","artifact_hash":"sha256:e1d44d99fdd284cad55d92ffc5f4da6570cbe70a04b8db13bc2386d9a3ef5d7d","generated_at":"2026-10-22T08:20:09.326997234Z"},"items":[{"id":"anthropic:claude-fable-5",
const newer = `{"data_version":{"generated_at":"2026-11-01T00:00:00Z"},"items":[{"id":"openai:gpt-6.1-sol",` +
	`"selected_offering":{"verified_at":"2026-10-30T00:00:00Z"},` +
	`"prices":{"input_tokens_per_1000000":"3","output_tokens_per_1000000":"12"}}]}`

// server serves body with an ETag, answering a request that names it with 304, and counts the requests.
type server struct {
	*httptest.Server
	mu   sync.Mutex
	body string
	hits atomic.Int32
}

// ref:   cost-dev/SOURCE
// quote: ETag "sha256:e1d44d99fdd284cad55d92ffc5f4da6570cbe70a04b8db13bc2386d9a3ef5d7d" (If-None-Match answers 304;
func newServer(t *testing.T, body string) *server {
	s := &server{body: body}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		s.mu.Lock()
		body := s.body
		s.mu.Unlock()
		tag := fmt.Sprintf(`"sha256:%x"`, sha256.Sum256([]byte(body)))
		if r.Header.Get("If-None-Match") == tag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", tag)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) serve(body string) {
	s.mu.Lock()
	s.body = body
	s.mu.Unlock()
}

func deps(s *server) Deps {
	return Deps{Client: func() *http.Client { return s.Client() }, URL: func() string { return s.URL }}
}

func sol(b *price.Book) price.Rate { return b.RateFor("gpt-6.1-sol", false, 0) }

func TestRefreshSwapsAndCaches(t *testing.T) {
	env := hosttest.Env(t)
	s := newServer(t, newer)
	p := New(env, deps(s))
	if p.Book().Version() != price.Default().Version() {
		t.Fatal("with no cache, the book isn't the snapshot's")
	}
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := sol(p.Book()); r.In != 3 || r.Out != 12 {
		t.Errorf("after a refresh gpt-6.1-sol = %+v, want the list's $3/$12", r)
	}
	var c cache
	data, err := os.ReadFile(filepath.Join(env.Home, "prices.json"))
	if err != nil || json.Unmarshal(data, &c) != nil || c.ETag == "" || string(c.Models) != newer {
		t.Fatalf("prices.json = %s (%v)", data, err)
	}
	if _, err := os.Stat(filepath.Join(env.Home, "prices.json.tmp")); !os.IsNotExist(err) {
		t.Error("the cache's temporary file is left")
	}

	// The next start has the cache's prices before it fetches anything.
	again := New(env, deps(s))
	if r := sol(again.Book()); r.In != 3 {
		t.Errorf("on the next start gpt-6.1-sol = %+v, want the cache's", r)
	}
	// It asks with the cache's ETag, and a 304 keeps the book.
	before := again.Book()
	if err := again.Refresh(context.Background()); err != nil || again.Book() != before {
		t.Errorf("a 304: err %v, book swapped %v", err, again.Book() != before)
	}
}

func TestBadListKeepsTheBook(t *testing.T) {
	env := hosttest.Env(t)
	s := newServer(t, newer)
	p := New(env, deps(s))
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	good := p.Book()
	for _, bad := range []string{`{"items":[`, `{"items":[]}`,
		`{"items":[{"id":"openai:gpt-6.1-sol","prices":{"input_tokens_per_1000000":"lots","output_tokens_per_1000000":"1"}}]}`} {
		s.serve(bad)
		if err := p.Refresh(context.Background()); err == nil {
			t.Errorf("%s: no error", bad)
		}
		if p.Book() != good {
			t.Errorf("%s: the book was swapped", bad)
		}
	}
	if c, _, err := p.readCache(); err != nil || string(c.Models) != newer {
		t.Errorf("the cache was overwritten: %s, %v", c.Models, err)
	}
}

// A cache older than the snapshot, or one that doesn't parse, is passed over for the snapshot.
func TestOldCacheIsPassedOver(t *testing.T) {
	env := hosttest.Env(t)
	old := `{"data_version":{"generated_at":"2026-01-01T00:00:00Z"},"items":[{"id":"openai:gpt-6.1-sol",` +
		`"prices":{"input_tokens_per_1000000":"3","output_tokens_per_1000000":"12"}}]}`
	for _, models := range []string{old, `{"items":[]}`} {
		data, _ := json.Marshal(cache{Models: json.RawMessage(models)})
		if err := os.WriteFile(filepath.Join(env.Home, "prices.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if p := New(env, Deps{}); p.Book().Version() != price.Default().Version() {
			t.Errorf("%s: the book isn't the snapshot's", models)
		}
	}
}

// Run fetches at once, then on a tick a day after the last fetch; after one fails, on a tick an hour after it.
func TestRunFetchesDaily(t *testing.T) {
	env := hosttest.Env(t)
	var mu sync.Mutex
	now := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	env.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	at := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	s := newServer(t, newer)
	p := New(env, deps(s))
	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.Run(ctx, tick)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	// A tick is taken once the one before it is done with: two in a row settle the first.
	settle := func() { tick <- time.Time{}; tick <- time.Time{} }
	hits := func(want int32) {
		t.Helper()
		settle()
		if n := s.hits.Load(); n != want {
			t.Errorf("fetches = %d, want %d", n, want)
		}
	}
	hits(1)
	at(23 * time.Hour)
	hits(1)
	at(time.Hour)
	hits(2)
	if r := sol(p.Book()); r.In != 3 {
		t.Errorf("gpt-6.1-sol = %+v", r)
	}

	s.serve("not json")
	at(24 * time.Hour)
	hits(3)
	at(59 * time.Minute)
	hits(3)
	at(time.Minute)
	hits(4)
}
