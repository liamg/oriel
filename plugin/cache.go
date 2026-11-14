package plugin

import (
	"container/list"
	"context"
	"os"
	"time"

	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/watch"
)

type CacheOpts[K comparable, V any] struct {
	MaxEntries int           // values kept; 0 is 256
	MaxBytes   int64         // their total Size; 0 is no bound
	Size       func(V) int64 // a value's bytes, for MaxBytes
	// Path is the file or folder a key is read from, or "": a change there, seen through Watches, makes its value
	// stale.
	Path func(K) string
	// Watches is the window's watch manager, which watches each Path as it watches every source: through the OS, by
	// polling when it can't, and paused in a background window. nil, or nil from it, and keys with a Path go by
	// MaxAge (2s when 0).
	Watches func() *watch.Manager
	// MaxAge is how old a value may be before the next Get fetches it again: for keys without a Path, or all of
	// them with no Watches. 0 keeps a value until Invalidate.
	MaxAge  time.Duration
	Timeout time.Duration // a fetch running longer fails; none when 0
	Clock   clock.Clock   // the window's clock (Host.UI) or the system's when nil
	Frame   func() int    // the frame being built (Host.UI's Frames) when nil
}

const (
	cacheEntries = 256
	pollAge      = 2 * time.Second // MaxAge for keys with a Path when nothing watches it
)

// Cache is a keyed cache whose values are fetched off the loop. Get never blocks: a miss starts a fetch and a
// stale value shows while it's fetched again. Identical asks share one fetch, and a fetch for a key no frame asked
// for since it started is cancelled. Values are dropped least recently used first, past MaxEntries or MaxBytes.
// Its methods are called on the loop; results land there through Do, with a Redraw.
type Cache[K comparable, V any] struct {
	h     Loop
	fetch func(context.Context, K) (V, error)
	o     CacheOpts[K, V]

	m     map[K]*cacheEntry[K, V]
	lru   list.List // of *cacheEntry, the most recently used first
	bytes int64
	frame int // the frame of the latest Get

	paths  map[string]*cachePath[K] // the paths read from, each watched while a key read from it is kept
	closed chan struct{}
}

// cachePath is a path keys were read from: the keys, and the watch on it.
type cachePath[K comparable] struct {
	keys map[K]bool
	sub  watch.Sub[fileSig]
}

// fileSig is what a path looked like on disk: a change to it is a new one.
type fileSig struct {
	exists bool
	size   int64
	mtime  int64
}

func sigOf(path string) fileSig {
	fi, err := os.Stat(path)
	if err != nil {
		return fileSig{}
	}
	return fileSig{exists: true, size: fi.Size(), mtime: fi.ModTime().UnixNano()}
}

type cacheEntry[K comparable, V any] struct {
	key   K
	el    *list.Element
	val   V
	has   bool
	size  int64
	state ui.LoadState
	at    time.Time // when the last fetch landed
	stale bool      // changed or invalidated since
	gen   int
	stop  context.CancelFunc // set while a fetch runs
	asked int                // the frame that last asked for it
	path  string
	sig   fileSig // path as the last fetch found it
	fsig  fileSig // path as the running fetch found it
}

func NewCache[K comparable, V any](h Loop, fetch func(context.Context, K) (V, error), o CacheOpts[K, V]) *Cache[K, V] {
	if o.MaxEntries <= 0 {
		o.MaxEntries = cacheEntries
	}
	return &Cache[K, V]{h: h, fetch: fetch, o: o, m: map[K]*cacheEntry[K, V]{}, paths: map[string]*cachePath[K]{}, closed: make(chan struct{})}
}

// Get is k's value and its load's state. A miss, or a stale value, starts a fetch.
func (c *Cache[K, V]) Get(k K) (V, ui.LoadState) {
	c.sweep()
	e := c.m[k]
	if e == nil {
		e = &cacheEntry[K, V]{key: k}
		e.el = c.lru.PushFront(e)
		c.m[k] = e
	} else {
		c.lru.MoveToFront(e.el)
	}
	e.asked = c.frame
	if e.stop == nil && (e.state.Phase == ui.Idle || c.isStale(e)) {
		c.start(e)
	}
	return e.val, e.state
}

// Fetching are the keys being fetched now.
func (c *Cache[K, V]) Fetching() []K {
	var out []K
	for k, e := range c.m {
		if e.stop != nil {
			out = append(out, k)
		}
	}
	return out
}

// Rereading are the paths whose change the watch is taking in: values read from them are about to go stale.
func (c *Cache[K, V]) Rereading() []string {
	var out []string
	for path, p := range c.paths {
		if p.sub.Valid() && p.sub.Busy() {
			out = append(out, path)
		}
	}
	return out
}

// Invalidate makes k's value stale: the next Get fetches it again, showing it meanwhile.
func (c *Cache[K, V]) Invalidate(k K) {
	if e := c.m[k]; e != nil {
		e.stale = true
	}
}

// Bytes is the total Size of the values held.
func (c *Cache[K, V]) Bytes() int64 { return c.bytes }

// Close cancels every fetch and stops watching.
func (c *Cache[K, V]) Close() {
	select {
	case <-c.closed:
		return
	default:
	}
	close(c.closed)
	for _, e := range c.m {
		c.cancel(e)
	}
	for _, p := range c.paths {
		if p.sub.Valid() {
			p.sub.Close()
		}
	}
}

func (c *Cache[K, V]) isStale(e *cacheEntry[K, V]) bool {
	if e.stale {
		return true
	}
	age := c.o.MaxAge
	if p := c.paths[e.path]; e.path != "" && p != nil && p.sub.Valid() {
		age = 0
	} else if e.path != "" && age == 0 {
		age = pollAge
	}
	return age > 0 && c.now().Sub(e.at) >= age
}

// sweep cancels, once a new frame asks, the fetches the frames before it didn't ask for.
func (c *Cache[K, V]) sweep() {
	f := c.curFrame()
	if f == c.frame {
		return
	}
	last := c.frame
	c.frame = f
	for _, e := range c.m {
		if e.stop != nil && e.asked < last {
			c.cancel(e)
		}
	}
}

func (c *Cache[K, V]) cancel(e *cacheEntry[K, V]) {
	if e.stop == nil {
		return
	}
	e.stop()
	e.stop = nil
	e.gen++
	switch {
	case e.has:
		e.state = ui.LoadState{Phase: ui.Ready, Err: e.state.Err, Progress: -1}
		e.stale = true
	default:
		e.state = ui.LoadState{}
	}
}

func (c *Cache[K, V]) start(e *cacheEntry[K, V]) {
	e.gen++
	gen := e.gen
	ctx, stop := context.WithCancel(context.Background())
	if c.o.Timeout > 0 {
		ctx, stop = context.WithTimeout(context.Background(), c.o.Timeout)
	}
	e.stop = stop
	e.state = ui.LoadState{Phase: ui.Pending, Since: c.now(), Err: e.state.Err, Stale: e.has, Progress: -1}
	if c.o.Path != nil && e.path == "" {
		e.path = c.o.Path(e.key)
	}
	k, path := e.key, e.path
	go func() {
		Chaos()
		var sig fileSig
		if path != "" {
			sig = sigOf(path) // before the read, so a change during it shows as one
		}
		c.h.Do(func() {
			if e.gen == gen {
				e.fsig = sig
			}
		})
		v, err := runFetch(ctx, c.o.Timeout, func(ctx context.Context) (V, error) { return c.fetch(ctx, k) })
		stop()
		c.h.Do(func() { c.land(e, gen, v, err) })
		c.h.Redraw()
	}()
}

func (c *Cache[K, V]) land(e *cacheEntry[K, V], gen int, v V, err error) {
	if e.gen != gen || c.m[e.key] != e {
		return
	}
	e.stop, e.stale, e.at, e.sig = nil, false, c.now(), e.fsig
	if e.path != "" {
		p := c.watchPath(e.path)
		p.keys[e.key] = true
		if p.sub.Valid() {
			if now, _, ok := p.sub.Get(); ok && now != e.sig {
				e.stale = true // the watch has seen it change since the read began
			}
		}
	}
	switch {
	case err == nil:
		var size int64
		if c.o.Size != nil {
			size = c.o.Size(v)
		}
		c.bytes += size - e.size
		e.val, e.has, e.size = v, true, size
		e.state = ui.LoadState{Phase: ui.Ready, Progress: -1}
	case e.has:
		e.state = ui.LoadState{Phase: ui.Ready, Err: err, Progress: -1}
	default:
		e.state = ui.LoadState{Phase: ui.Failed, Err: err, Progress: -1}
	}
	c.evict()
}

// evict drops the least recently used values past the bounds, never one being fetched or the newest.
func (c *Cache[K, V]) evict() {
	over := func() bool {
		return c.lru.Len() > c.o.MaxEntries || c.o.MaxBytes > 0 && c.bytes > c.o.MaxBytes
	}
	for el := c.lru.Back(); el != nil && el != c.lru.Front() && over(); {
		e := el.Value.(*cacheEntry[K, V])
		el = el.Prev()
		if e.stop != nil {
			continue
		}
		c.drop(e)
	}
}

func (c *Cache[K, V]) drop(e *cacheEntry[K, V]) {
	c.lru.Remove(e.el)
	delete(c.m, e.key)
	c.bytes -= e.size
	if p := c.paths[e.path]; p != nil {
		delete(p.keys, e.key)
		if len(p.keys) == 0 {
			delete(c.paths, e.path)
			if p.sub.Valid() {
				p.sub.Close()
			}
		}
	}
}

// watchPath is path's entry, watched through Watches from the first key read from it; on the loop.
func (c *Cache[K, V]) watchPath(path string) *cachePath[K] {
	if p := c.paths[path]; p != nil {
		return p
	}
	p := &cachePath[K]{keys: map[K]bool{}}
	c.paths[path] = p
	var m *watch.Manager
	if c.o.Watches != nil {
		m = c.o.Watches()
	}
	if m == nil {
		return p
	}
	src := watch.Source[fileSig]{Key: "cache:" + path, Files: []watch.Files{{Path: path}},
		Same: func(a, b fileSig) bool { return a == b },
		Read: func(watch.Reader) (fileSig, error) { return sigOf(path), nil }}
	p.sub = watch.SubscribeFunc(m, src, watch.Need{}, "a cached read", func() {
		c.h.Do(func() { c.changed(path) })
		c.h.Redraw()
	})
	return p
}

// changed makes stale the values read from path that the watch's reading of it no longer matches.
func (c *Cache[K, V]) changed(path string) {
	p := c.paths[path]
	if p == nil || !p.sub.Valid() {
		return
	}
	now, _, ok := p.sub.Get()
	if !ok {
		return
	}
	for k := range p.keys {
		if e := c.m[k]; e != nil && e.sig != now {
			e.stale = true
		}
	}
}

func (c *Cache[K, V]) now() time.Time { return loopNow(c.h, c.o.Clock) }

func (c *Cache[K, V]) curFrame() int {
	if c.o.Frame != nil {
		return c.o.Frame()
	}
	if h, ok := c.h.(interface{ UI() UIView }); ok {
		if u := h.UI(); u != nil {
			return u.Frames()
		}
	}
	return 0
}
