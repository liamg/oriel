package kit

import (
	"container/list"
	"sync"
)

// Memo keeps what's made from each key, the max most recently used: work done once per source, such as a
// markdown document's parse.
type Memo[K comparable, V any] struct {
	mu  sync.Mutex
	max int
	m   map[K]*list.Element
	lru list.List // of memoEntry, the most recently used first
}

type memoEntry[K comparable, V any] struct {
	k K
	v V
}

func NewMemo[K comparable, V any](max int) *Memo[K, V] {
	return &Memo[K, V]{max: max, m: map[K]*list.Element{}}
}

// Get is what make made from k, made now if it isn't kept.
func (m *Memo[K, V]) Get(k K, make func() V) V {
	m.mu.Lock()
	if el, ok := m.m[k]; ok {
		m.lru.MoveToFront(el)
		m.mu.Unlock()
		return el.Value.(memoEntry[K, V]).v
	}
	m.mu.Unlock()
	v := make()
	m.mu.Lock()
	defer m.mu.Unlock()
	if el, ok := m.m[k]; ok {
		m.lru.MoveToFront(el)
		return el.Value.(memoEntry[K, V]).v
	}
	m.m[k] = m.lru.PushFront(memoEntry[K, V]{k, v})
	for m.lru.Len() > m.max {
		last := m.lru.Back()
		delete(m.m, last.Value.(memoEntry[K, V]).k)
		m.lru.Remove(last)
	}
	return v
}
