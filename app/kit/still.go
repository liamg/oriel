package kit

import (
	"sync"
	"time"
)

// Still holds back costly work on something that changes version by version, a buffer as it's typed in, until it
// has stayed at one version for a while: Ready says when, and wakes calls wake once the wait is over, so the frame
// that asks again finds it ready. The zero Still is ready at once.
type Still[K comparable] struct {
	Wait time.Duration
	Wake func()

	mu   sync.Mutex
	seen map[K]stillAt
}

type stillAt struct {
	version uint64
	at      time.Time
	waking  bool
}

// Ready says k has been at version for Wait; if not, Wake is called once it has, unless it changes first.
func (s *Still[K]) Ready(k K, version uint64) bool {
	if s.Wait <= 0 {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[K]stillAt{}
	}
	at, ok := s.seen[k]
	if !ok || at.version != version {
		at = stillAt{version: version, at: time.Now()}
	}
	wait := s.Wait - time.Since(at.at)
	if wait <= 0 {
		s.seen[k] = at
		return true
	}
	if !at.waking && s.Wake != nil {
		at.waking = true
		time.AfterFunc(wait, func() {
			s.mu.Lock()
			if cur, ok := s.seen[k]; ok && cur.version == version {
				cur.waking = false
				s.seen[k] = cur
			}
			s.mu.Unlock()
			s.Wake()
		})
	}
	s.seen[k] = at
	return false
}

// Held says some work is waiting for a thing to be still.
func (s *Still[K]) Held() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, at := range s.seen {
		if at.waking {
			return true
		}
	}
	return false
}

// Forget drops k, gone.
func (s *Still[K]) Forget(k K) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, k)
}
