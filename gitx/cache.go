package gitx

import (
	"sync"
)

// Cache is what Repos sharing it keep between calls: a long-lived git cat-file --batch per repo, merge-bases,
// user.email, and commits' Co-Authored-By readings. Its owner closes it, which stops its gits; a Repo with none
// asks git afresh each time.
type Cache struct {
	mu         sync.Mutex
	closed     bool
	batches    map[string]*catFile
	mergeBases map[mergeBaseKey]mergeBase
	emails     map[string]email
	trailers   map[string]bool
}

// NewCache is an empty Cache.
func NewCache() *Cache {
	return &Cache{batches: map[string]*catFile{}, mergeBases: map[mergeBaseKey]mergeBase{}, emails: map[string]email{},
		trailers: map[string]bool{}}
}

// Close stops every git the cache started and waits for them; a Repo using it after reads without it.
func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	batches := c.batches
	c.batches = map[string]*catFile{}
	c.mu.Unlock()
	for _, b := range batches {
		b.close()
	}
}

// batch is the cat-file for key, or nil once the cache is closed.
func (c *Cache) batch(key string) *catFile {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	b := c.batches[key]
	if b == nil {
		b = &catFile{held: make(chan struct{}, 1)}
		c.batches[key] = b
	}
	return b
}
