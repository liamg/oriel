// Package eventq is an unbounded queue in front of a session's Events channel, so nothing a provider does waits on
// whoever reads them.
package eventq

import (
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
)

// Queue holds what's pushed until Out's reader takes it.
type Queue struct {
	mu     sync.Mutex
	items  []agent.Event
	signal chan struct{}
	closed bool
	out    chan agent.Event
}

// New starts a queue; Close ends it.
func New() *Queue {
	q := &Queue{signal: make(chan struct{}, 1), out: make(chan agent.Event, 64)}
	go q.pump()
	return q
}

// Out is the channel the events come out on, closed once Close's events are delivered.
func (q *Queue) Out() <-chan agent.Event { return q.out }

// Push queues ev; after Close it's dropped.
func (q *Queue) Push(ev agent.Event) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, ev)
	}
	q.mu.Unlock()
	q.wake()
}

// Close ends Out once what's queued is delivered, or dropped if nobody is reading.
func (q *Queue) Close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.wake()
}

func (q *Queue) wake() {
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *Queue) pump() {
	for range q.signal {
		for {
			q.mu.Lock()
			if len(q.items) == 0 {
				closed := q.closed
				q.mu.Unlock()
				if closed {
					close(q.out)
					return
				}
				break
			}
			ev := q.items[0]
			q.items = q.items[1:]
			closed := q.closed
			q.mu.Unlock()
			if closed {
				select {
				case q.out <- ev:
				case <-time.After(time.Second):
				}
				continue
			}
			q.out <- ev
		}
	}
}
