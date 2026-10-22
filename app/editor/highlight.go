package editor

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim"
)

// highlighter keeps a buffer's syntax highlights up to date, for the panes showing it.
type highlighter struct {
	doc     *syntax.Document
	lang    string
	cancel  func()
	pending []text.Change // edits made while its document was being made, for it once it's ready
	closed  bool
}

func (h *highlighter) close() {
	h.closed = true
	if h.cancel != nil {
		h.cancel()
	}
	if h.doc != nil {
		h.doc.Close()
	}
}

// highlights is the buffer's highlighter, started the first time a pane draws it.
func (x *Editor) highlights(p vim.Pane) *highlighter {
	b := p.Buffer()
	if h, ok := x.docs[b]; ok {
		return h
	}
	h := &highlighter{}
	x.docs[b] = h
	lang, ok := kit.LanguageFor(x.h, p.Name(), b.Snapshot().Bytes())
	if !ok {
		return h
	}
	// Making a parser instantiates the grammar's wasm (tens of milliseconds, a first compile longer), so it's made
	// off the loop: the file shows plain for a frame or two, and edits made meanwhile wait for it.
	h.lang = lang.Name
	snap := b.Snapshot()
	h.cancel = b.Subscribe(func(ch text.Change) {
		if h.doc != nil {
			h.doc.Change(ch)
		} else {
			h.pending = append(h.pending, ch)
		}
	})
	name := p.Name()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		doc, err := syntax.NewDocument(lang, snap)
		if err == nil {
			x.madeMu.Lock()
			if x.stopped {
				x.madeMu.Unlock()
				doc.Close()
				return
			}
			if x.made == nil {
				x.made = map[*syntax.Document]bool{}
			}
			x.made[doc] = true
			x.madeMu.Unlock()
		}
		x.h.Do(func() {
			if err != nil {
				x.h.Log().Warn("syntax", "file", name, "err", err)
				return
			}
			x.madeMu.Lock()
			delete(x.made, doc)
			x.madeMu.Unlock()
			if h.closed {
				doc.Close()
				return
			}
			doc.OnUpdate = x.h.Redraw
			for _, ch := range h.pending {
				doc.Change(ch)
			}
			h.pending, h.doc = nil, doc
			x.h.Redraw()
		})
	}()
	return h
}
