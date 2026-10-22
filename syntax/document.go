package syntax

import (
	"slices"
	"sort"
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
	// querying holds off the next parse while Query reads the tree.
	querying bool
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
		for len(d.pending) == 0 && !d.closed || d.querying {
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

		var dirty []text.Range // what to highlight again, in the latest text
		for _, ch := range chs {
			for _, r := range ch.Replaces {
				dirty = shiftRanges(dirty, r)
				dirty = append(dirty, text.Range{Start: r.Range.Start, End: r.Range.Start + len(r.Text)})
			}
			d.p.Update(ch)
			dirty = append(dirty, d.p.Changed()...)
		}
		last := chs[len(chs)-1].After
		d.mu.Lock()
		old := d.spans
		d.mu.Unlock()
		spans, err := d.rehighlight(shiftSpans(old, chs), lines(last, dirty))

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
	// Only spans within reach of [start, end) can land in it: the edits move text by at most reach.
	reach := 0
	for _, ch := range moving {
		for _, r := range ch.Replaces {
			reach += max(len(r.Text), r.Range.Len())
		}
	}
	spans = within(spans, start-reach, end+reach)
	if len(moving) > 0 {
		spans = within(shiftSpans(spans, moving), start, end)
	}
	return slices.Clone(spans), err
}

// within is the spans that overlap [start, end), found by bisecting spans, which are in order and don't overlap.
func within(spans []Span, start, end int) []Span {
	lo := sort.Search(len(spans), func(i int) bool { return spans[i].Range.End > start })
	hi := sort.Search(len(spans), func(i int) bool { return spans[i].Range.Start >= end })
	return spans[lo:max(lo, hi)]
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

// rehighlight is spans with the dirty ranges highlighted again: spans outside them kept, cut at their edges.
func (d *Document) rehighlight(spans []Span, dirty []text.Range) ([]Span, error) {
	var out []Span
	i := 0
	var err error
	for _, r := range dirty {
		for i < len(spans) && spans[i].Range.Start < r.Start {
			s := spans[i]
			s.Range.End = min(s.Range.End, r.Start)
			out = append(out, s)
			if spans[i].Range.End > r.End { // it runs past r: what's after r is kept
				spans[i].Range.Start = r.End
				break
			}
			i++
		}
		fresh, e := d.p.highlightsIn(r.Start, r.End)
		if e != nil {
			err = e
		}
		out = append(out, fresh...)
		for ; i < len(spans) && spans[i].Range.Start < r.End; i++ {
			if s := spans[i]; s.Range.End > r.End {
				s.Range.Start = r.End
				spans[i] = s
				break
			}
		}
	}
	out = append(out, spans[i:]...)
	sort.SliceStable(out, func(a, b int) bool { return out[a].Range.Start < out[b].Range.Start })
	return out, err
}

// lines is ranges widened to whole lines of s, sorted and merged.
func lines(s text.Snapshot, rs []text.Range) []text.Range {
	for i, r := range rs {
		a := s.LineStart(s.PosAt(min(r.Start, s.Len())).Line)
		end := s.PosAt(min(r.End, s.Len())).Line
		b := s.Len()
		if end+1 < s.Lines() {
			b = s.LineStart(end + 1)
		}
		rs[i] = text.Range{Start: a, End: b}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
	var out []text.Range
	for _, r := range rs {
		if k := len(out) - 1; k >= 0 && r.Start <= out[k].End {
			out[k].End = max(out[k].End, r.End)
			continue
		}
		out = append(out, r)
	}
	return out
}

// shiftRanges moves ranges through a replace made after them, growing one it touches to cover what's there now.
func shiftRanges(rs []text.Range, r text.Replace) []text.Range {
	from, to, n := r.Range.Start, r.Range.End, len(r.Text)
	move := func(off int) int {
		switch {
		case off <= from:
			return off
		case off >= to:
			return off + n - (to - from)
		}
		return from + n
	}
	for i := range rs {
		rs[i] = text.Range{Start: move(rs[i].Start), End: move(rs[i].End)}
	}
	return rs
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

// NodePath is the parser's NodePath once every queued change is parsed. Call it from the goroutine that makes the
// changes, so none come in meanwhile.
func (d *Document) NodePath(off int) []string {
	d.Wait()
	return d.p.NodePath(off)
}
