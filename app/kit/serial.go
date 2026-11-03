package kit

import "sync"

// Serial runs what it's handed off the loop, one at a time in the order handed: calls that mustn't reorder (a
// watcher's) and mustn't hold up whoever hands them over (a frame's build). Stop drops what's waiting.
type Serial struct {
	mu      sync.Mutex
	queue   []func()
	running bool
	stopped bool
	wg      sync.WaitGroup
}

// Do queues fn; nothing once stopped.
func (s *Serial) Do(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.queue = append(s.queue, fn)
	if !s.running {
		s.running = true
		s.wg.Add(1)
		go s.run()
	}
}

func (s *Serial) run() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 || s.stopped {
			s.queue, s.running = nil, false
			s.mu.Unlock()
			return
		}
		fn := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()
		fn()
	}
}

// Wait waits until nothing's queued or running.
func (s *Serial) Wait() { s.wg.Wait() }

// Stop drops what's queued, and waits for the one running.
func (s *Serial) Stop() {
	s.mu.Lock()
	s.stopped, s.queue = true, nil
	s.mu.Unlock()
	s.wg.Wait()
}
