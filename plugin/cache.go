package plugin

import (
	"container/list"
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/ui"
)

type CacheOpts[K comparable, V any] struct {
	MaxEntries int           // values kept; 0 is 256
	MaxBytes   int64         // their total Size; 0 is no bound
	Size       func(V) int64 // a value's bytes, for MaxBytes
	// Path is the file or folder a key is read from, or "": a change there, seen by the OS's watcher, makes its
	// value stale.
	Path func(K) string
	// MaxAge is how old a value may be before the next Get fetches it again: for keys without a Path, or all of
	// them when the OS can't watch (then 2s when 0). 0 keeps a value until Invalidate.
	MaxAge  time.Duration
	Timeout time.Duration    // a fetch running longer fails; none when 0
	Now     func() time.Time // the window's clock (Host.UI) or time.Now when nil
	Frame   func() int       // the frame being built (Host.UI's Frames) when nil
	Watch   func() (*fswatch.Watcher, error)
}

const (
	cacheEntries = 256
	pollAge      = 2 * time.Second // MaxAge for watched keys once the OS can't watch
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

	watch     sync.Once
	wmu       sync.Mutex
	w         *fswatch.Watcher // nil until a watched key is fetched, or when the OS can't watch
	unwatched bool             // the OS can't watch: watched keys go by MaxAge
	paths     map[string]map[K]bool
	closed    chan struct{}
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
}

func NewCache[K comparable, V any](h Loop, fetch func(context.Context, K) (V, error), o CacheOpts[K, V]) *Cache[K, V] {
	if o.MaxEntries <= 0 {
		o.MaxEntries = cacheEntries
	}
	if o.Watch == nil {
		o.Watch = fswatch.New
	}
	return &Cache[K, V]{h: h, fetch: fetch, o: o, m: map[K]*cacheEntry[K, V]{}, paths: map[string]map[K]bool{}, closed: make(chan struct{})}
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
	c.watch.Do(func() {})
	if w := c.watcher(); w != nil {
		w.Close()
	}
}

func (c *Cache[K, V]) isStale(e *cacheEntry[K, V]) bool {
	if e.stale {
		return true
	}
	age := c.o.MaxAge
	if e.path != "" && !c.unwatched {
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
		if path != "" {
			c.watchPath(path)
		}
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
	e.stop, e.stale, e.at = nil, false, c.now()
	if e.path != "" {
		if c.paths[e.path] == nil {
			c.paths[e.path] = map[K]bool{}
		}
		c.paths[e.path][e.key] = true
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
	if ks := c.paths[e.path]; ks != nil {
		delete(ks, e.key)
		if len(ks) == 0 {
			delete(c.paths, e.path)
			if w := c.watcher(); w != nil {
				go w.Remove(e.path)
			}
		}
	}
}

// watchPath watches p, starting the watcher the first time; off the loop.
func (c *Cache[K, V]) watchPath(p string) {
	c.watch.Do(func() {
		w, err := c.o.Watch()
		if err != nil {
			c.h.Do(func() { c.unwatched = true })
			return
		}
		c.wmu.Lock()
		c.w = w
		c.wmu.Unlock()
		go c.changes(w)
	})
	if w := c.watcher(); w != nil && w.Add(p) != nil {
		c.h.Do(func() { c.unwatched = true })
	}
}

func (c *Cache[K, V]) watcher() *fswatch.Watcher {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.w
}

func (c *Cache[K, V]) changes(w *fswatch.Watcher) {
	for ev := range w.Events() {
		c.h.Do(func() {
			if ev.Err != nil {
				c.unwatched = true
			}
			c.changed(ev.Path)
			ev.Done() // taken in: a test's scripted change waits for it
		})
		c.h.Redraw()
	}
}

// changed makes stale the values read from p, or from the folder it's in.
func (c *Cache[K, V]) changed(p string) {
	for _, q := range []string{p, filepath.Dir(p)} {
		for k := range c.paths[q] {
			if e := c.m[k]; e != nil {
				e.stale = true
			}
		}
	}
}

func (c *Cache[K, V]) now() time.Time { return loopNow(c.h, c.o.Now) }

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
