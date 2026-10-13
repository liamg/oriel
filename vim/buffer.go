package vim

import (
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/text"
)

// SetModified sets whether the current buffer has changes not written, as 'modified'.
func (e *Engine) SetModified(on bool) { e.buffer.modified = on }

// emptyBuffer makes a new buffer with no text and no name.
func (e *Engine) emptyBuffer() *buffer { return e.newBuffer(text.New(nil, text.Author{})) }

// switchBuffer shows b in the current window, remembering where the cursor was in the buffer it leaves.
// Without 'hidden', a changed buffer isn't left unless force is set, which unloads it.
func (e *Engine) switchBuffer(b *buffer, force bool) error {
	if b == e.buffer {
		return nil
	}
	old := e.buffer
	if !e.opts.Hidden && old.modified && !e.shownElsewhere(old) {
		if !force {
			return errUnsaved.with()
		}
		defer e.unload(old)
	}
	old.last = e.cur
	e.window.alt = old
	e.window.ed = e.ed.Open(b.text)
	e.window.buf = b
	e.buffer = b
	e.cur = b.last
	v := e.view()
	e.clamp(v)
	e.setWant(v)
	return nil
}

func (e *Engine) shownElsewhere(b *buffer) bool {
	for _, w := range e.order() {
		if w.buf == b && w != e.window {
			return true
		}
	}
	return false
}

// unload drops a buffer's text: with no file to read it back from, it is empty when next edited.
func (e *Engine) unload(b *buffer) {
	t := text.New(nil, text.Author{})
	b.text = t
	b.modified = false
	b.marks, b.undoCur, b.undoMarks, b.uLine = map[rune]Pos{}, map[int]Pos{}, map[int]map[rune]Pos{}, -1
	b.last = Pos{}
	b.lastVisual = nil
	t.Subscribe(func(ch text.Change) {
		b.modified = true
		e.adjustMarks(b, ch)
	})
}

// listed is the buffers in :ls order.
func (e *Engine) listed() []*buffer {
	var out []*buffer
	for _, b := range e.buffers {
		if !b.unlisted {
			out = append(out, b)
		}
	}
	return out
}

// bufferByNum finds a buffer, listed or not, by its number.
func (e *Engine) bufferByNum(n int) *buffer {
	for _, b := range e.buffers {
		if b.num == n {
			return b
		}
	}
	return nil
}

// alternate is CTRL-^: the alternate buffer, or with a count that buffer.
func (e *Engine) alternate(count int) {
	b := e.window.alt
	if count > 0 {
		if b = e.bufferByNum(count); b == nil {
			e.notify(errBufferNotFound.with(count))
			return
		}
	} else if b == nil {
		e.notify(errNoAlternate.with())
		return
	}
	b.unlisted = false
	if err := e.switchBuffer(b, false); err != nil {
		e.fail(err)
	}
}

// exBuffer runs the buffer commands.
func (e *Engine) exBuffer(name, args string, r exRange, bang bool) error {
	args = strings.TrimSpace(args)
	if strings.HasPrefix(name, "sb") {
		e.split(false, nil)
		name = strings.TrimPrefix(name, "s")
	}
	count := 1
	if r.given > 0 {
		count = r.to + 1
	}
	if n, err := strconv.Atoi(args); err == nil && n > 0 {
		count = n
	}
	listed := e.listed()
	idx := slices.Index(listed, e.buffer)
	step := func(n int) error {
		if len(listed) == 0 {
			return nil
		}
		if idx < 0 {
			idx = 0
		}
		return e.switchBuffer(listed[((idx+n)%len(listed)+len(listed))%len(listed)], bang)
	}
	switch name {
	case "enew":
		b := e.buffer
		if b.name == "" && !b.modified && b.text.Snapshot().Len() == 0 {
			return nil
		}
		if !e.opts.Hidden && b.modified && !bang && !e.shownElsewhere(b) {
			return errUnsaved.with()
		}
		return e.switchBuffer(e.emptyBuffer(), bang)
	case "bnext":
		return step(count)
	case "bprevious", "bNext":
		return step(-count)
	case "bfirst", "brewind":
		return e.switchBuffer(listed[0], bang)
	case "blast":
		return e.switchBuffer(listed[len(listed)-1], bang)
	case "bmodified":
		for i := 1; i <= len(e.buffers); i++ {
			b := e.buffers[(slices.Index(e.buffers, e.buffer)+i)%len(e.buffers)]
			if b.modified && b != e.buffer && !b.unlisted {
				return e.switchBuffer(b, bang)
			}
		}
		return errNoModified.with()
	case "buffer":
		b, err := e.findBuffer(args, r)
		if err != nil || b == nil {
			return err
		}
		return e.switchBuffer(b, bang)
	case "badd", "balt":
		b := e.emptyBuffer()
		b.name = args
		if name == "balt" {
			e.window.alt = b
		}
	case "bdelete", "bwipeout", "bunload":
		b := e.buffer
		if args != "" {
			if n, err := strconv.Atoi(args); err == nil {
				if b = e.bufferByNum(n); b == nil {
					return errNoneDeleted.with()
				}
			} else if b, err = e.findBuffer(args, exRange{}); err != nil {
				return err
			}
		} else if r.given > 0 {
			if b = e.bufferByNum(r.to + 1); b == nil {
				return errNoneDeleted.with()
			}
		}
		if b.modified && !bang {
			return errUnsavedBuffer.with(b.num)
		}
		e.deleteBuffer(b, name == "bwipeout", name == "bunload")
	case "edit":
		switch args {
		case "#":
			if e.window.alt == nil {
				return errNoAlternate.with()
			}
			if e.window.alt.name == "" {
				return errNoFileName.with()
			}
			return e.switchBuffer(e.window.alt, bang)
		case "":
			if bang {
				e.unloadCurrent()
			}
			return nil
		}
		for _, b := range e.buffers {
			if b.name == args {
				b.unlisted = false
				return e.switchBuffer(b, bang)
			}
		}
		b := e.emptyBuffer()
		b.name = args
		return e.switchBuffer(b, bang)
	case "ls", "buffers", "files":
		for _, b := range listed {
			flags := " h"
			switch {
			case b == e.buffer:
				flags = "%a"
			case b == e.window.alt:
				flags = "#h"
			case e.visible(b):
				flags = " a"
			}
			line := b.last.Line + 1
			if b == e.buffer {
				line = e.cur.Line + 1
			}
			e.notify(msgBufferLine.with(b.num, flags, flag(b.modified), b.displayName(), line))
		}
	case "file":
		if args != "" {
			if e.buffer.name != "" {
				old := e.emptyBuffer()
				old.name, old.unlisted = e.buffer.name, true
				e.window.alt = old
			}
			e.buffer.name = args
			e.buffer.notEdited = true
		}
		e.fileInfo()
	}
	return nil
}

// unloadCurrent is :e!: the buffer goes back to what is on disk, which with no file is nothing.
func (e *Engine) unloadCurrent() {
	v := e.view()
	e.do(e.newGroup(), text.Replace{Range: text.Range{Start: 0, End: v.s.Len()}})
	e.buffer.modified = false
	e.cur = Pos{}
}

func (b *buffer) displayName() string {
	if b.name == "" {
		return "[No Name]"
	}
	return b.name
}

// fileInfo is CTRL-G and :file: the buffer's name, state and where the cursor is in it.
func (e *Engine) fileInfo() {
	b := e.buffer
	v := e.view()
	n, pct := v.lines(), (e.cur.Line+1)*100/v.lines()
	if v.s.Len() == 0 {
		n = 0
	}
	e.notify(msgFileInfo.with(b.displayName(), flag(b.modified), flag(b.notEdited), n, pct))
}

func (e *Engine) visible(b *buffer) bool {
	for _, w := range e.order() {
		if w.buf == b {
			return true
		}
	}
	return false
}

// findBuffer finds a buffer by number or by a unique part of its name.
func (e *Engine) findBuffer(arg string, r exRange) (*buffer, error) {
	if arg == "" {
		if r.given > 0 {
			if b := e.bufferByNum(r.to + 1); b != nil {
				return b, nil
			}
			return nil, errNoBuffer.with(r.to + 1)
		}
		return nil, nil
	}
	if arg == "#" {
		if e.window.alt == nil {
			return nil, errNoAlternate.with()
		}
		return e.window.alt, nil
	}
	if arg == "%" {
		return e.buffer, nil
	}
	if n, err := strconv.Atoi(arg); err == nil {
		if b := e.bufferByNum(n); b != nil {
			return b, nil
		}
		return nil, errNoBuffer.with(n)
	}
	var found []*buffer
	for _, b := range e.listed() {
		if b.name == arg {
			return b, nil
		}
		if strings.Contains(b.name, arg) {
			found = append(found, b)
		}
	}
	switch len(found) {
	case 0:
		return nil, errNoBufferMatch.with(arg)
	case 1:
		return found[0], nil
	}
	return nil, errManyBufferMatch.with(arg)
}

// deleteBuffer unloads and unlists a buffer (wipe: forgets it); windows showing it move to the alternate
// buffer, or another listed one, or a new one.
func (e *Engine) deleteBuffer(b *buffer, wipe, keepListed bool) {
	cur := e.window
	for _, w := range e.order() {
		if w.buf != b {
			continue
		}
		var next *buffer
		if w.alt != nil && w.alt != b && !w.alt.unlisted {
			next = w.alt
		}
		if next == nil {
			for _, o := range e.listed() {
				if o != b {
					next = o
					break
				}
			}
		}
		if next == nil {
			next = e.emptyBuffer()
		}
		e.window, e.buffer = w, w.buf
		b.modified = false
		e.switchBuffer(next, true)
		if w == cur {
			cur = e.window
		}
	}
	e.window, e.buffer = cur, cur.buf
	e.unload(b)
	b.unlisted = !keepListed
	if wipe {
		e.buffers = slices.DeleteFunc(e.buffers, func(x *buffer) bool { return x == b })
		for _, w := range e.windows {
			if w.alt == b {
				w.alt = nil
			}
		}
	}
}
