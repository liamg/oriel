package syntax

import (
	"sync"

	"github.com/liamg/oriel/text"
)

// Document keeps a buffer's syntax up to date on a goroutine of its own, so no keystroke waits on a parse.
// Readers get the highlights of the last finished parse, moved through the changes made since.
type Document struct {
	mu      sync.Mutex
	cond    *sync.Cond
	p       *Parser
	pending []text.Change
	version uint64 // the snapshot version spans were computed for
	spans   []Span
	err     error
	busy    bool
	closed  bool
	// OnUpdate, when set, is called on the document's goroutine after each parse, to repaint.
	OnUpdate func()
}

// NewDocument parses a snapshot and starts keeping it up to date.
func NewDocument(l *Language, s text.Snapshot) (*Document, error) {
	p, err := NewParser(l)
	if err != nil {
		return nil, err
	}
	d := &Document{p: p}
	d.cond = sync.NewCond(&d.mu)
	p.ParseSnapshot(s)
	d.spans, d.err = p.Highlights(0, s.Len())
	d.version = s.Version()
	go d.run()
	return d, nil
}

// Change queues a buffer change and returns at once; the reparse happens in the background.
func (d *Document) Change(ch text.Change) {
	d.mu.Lock()
	d.pending = append(d.pending, ch)
	d.mu.Unlock()
	d.cond.Broadcast()
}

func (d *Document) run() {
	for {
		d.mu.Lock()
		for len(d.pending) == 0 && !d.closed {
			d.cond.Wait()
		}
		if d.closed {
			d.mu.Unlock()
			d.p.Close()
			return
		}
		chs := d.pending
		d.pending = nil
		d.busy = true
		d.mu.Unlock()

		for _, ch := range chs {
			d.p.Update(ch)
		}
		last := chs[len(chs)-1].After
		spans, err := d.p.Highlights(0, last.Len())

		d.mu.Lock()
		d.spans, d.err, d.version, d.busy = spans, err, last.Version(), false
		fn := d.OnUpdate
		d.mu.Unlock()
		d.cond.Broadcast()
		if fn != nil {
			fn()
		}
	}
}

// Highlights returns the spans in [start, end) of the latest snapshot the document was told about. Spans from
// before changes still being parsed are moved through them, so they line up with the text, if a little stale.
func (d *Document) Highlights(start, end int) ([]Span, error) {
	d.mu.Lock()
	spans, err, pending := d.spans, d.err, d.pending
	var moving []text.Change
	for _, ch := range pending {
		if ch.Before.Version() >= d.version {
			moving = append(moving, ch)
		}
	}
	d.mu.Unlock()
	if len(moving) > 0 {
		spans = shiftSpans(spans, moving)
	}
	var out []Span
	for _, s := range spans {
		if s.Range.End > start && s.Range.Start < end {
			out = append(out, s)
		}
	}
	return out, err
}

// Wait blocks until every queued change is parsed.
func (d *Document) Wait() {
	d.mu.Lock()
	for len(d.pending) > 0 || d.busy {
		d.cond.Wait()
	}
	d.mu.Unlock()
}

// Close stops the document's goroutine and frees its parser.
func (d *Document) Close() {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	d.cond.Broadcast()
}

// shiftSpans moves spans through changes made after they were computed: text after an edit moves with it,
// and a span the edit touches stretches or shrinks to cover what's there now.
func shiftSpans(spans []Span, chs []text.Change) []Span {
	out := append([]Span(nil), spans...)
	for _, ch := range chs {
		for _, r := range ch.Replaces {
			from, to, n := r.Range.Start, r.Range.End, len(r.Text)
			move := func(off int, isEnd bool) int {
				switch {
				case off < from || off == from && !isEnd:
					return off
				case off >= to && (off > to || isEnd):
					return off + n - (to - from)
				case isEnd:
					return from + n
				}
				return from
			}
			kept := out[:0]
			for _, s := range out {
				s.Range.Start, s.Range.End = move(s.Range.Start, false), move(s.Range.End, true)
				if s.Range.Start < s.Range.End {
					kept = append(kept, s)
				}
			}
			out = kept
		}
	}
	return out
}
