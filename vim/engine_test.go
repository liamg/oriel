package vim_test

import (
	"strings"

	"github.com/liamg/oriel/app/kit/vimreg"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim"
	"github.com/liamg/oriel/vim/vimtest"
	"github.com/liamg/oriel/vim/vimtest/nvimtext"
)

// testEngine runs vimtest cases against the engine.
type testEngine struct {
	ed *edit.Editor
	e  *vim.Engine
}

func newTestEngine() (vimtest.Engine, error) { return &testEngine{}, nil }

func (t *testEngine) Load(b vimtest.Buffer) error {
	buf := text.New([]byte(strings.Join(b.Lines, "\n")), text.Author{})
	t.ed = edit.New(buf, text.Author{Kind: text.User})
	t.e = vim.New(t.ed)
	// The engine's options come from the registry, as they do in the IDE.
	r := registry.New()
	vimreg.Register(r)
	t.e.SetSettings(vimreg.Settings{Options: r.Options()})
	if b.Visual != nil {
		t.e.SetCursor(vim.Pos{Line: b.Visual.Anchor.Line, Col: b.Visual.Anchor.Col})
		t.e.SetVisual(t.e.Cursor())
	}
	t.e.SetCursor(vim.Pos{Line: b.Cursor.Line, Col: b.Cursor.Col})
	t.e.SetModified(true)
	return nil
}

func (t *testEngine) Feed(keys []vimtest.Key) error {
	for _, k := range keymap.Parse(vimtest.Notation(keys)) {
		t.e.Key(k)
	}
	return nil
}

func (t *testEngine) Snapshot(q vimtest.Query) (vimtest.Result, error) {
	r := vimtest.Result{Source: "engine", Mode: t.e.Mode()}
	snap := t.e.Buf()
	for i := range snap.Lines() {
		r.Lines = append(r.Lines, string(snap.Line(i)))
	}
	c := t.e.Cursor()
	r.Cursor = vimtest.Pos{Line: c.Line, Col: c.Col}
	if a, ok := t.e.Visual(); ok {
		v := &vimtest.Visual{Start: vimtest.Pos{Line: a.Line, Col: a.Col}, End: r.Cursor, Anchor: vimtest.Pos{Line: a.Line, Col: a.Col}}
		if v.End.Line < v.Start.Line || v.End.Line == v.Start.Line && v.End.Col < v.Start.Col {
			v.Start, v.End = v.End, v.Start
		}
		r.Visual = v
	}
	if len(q.Regs) > 0 {
		r.Regs = map[rune]*string{}
	}
	for _, name := range q.Regs {
		if s, ok := t.e.Register(name); ok {
			r.Regs[name] = &s
		} else {
			r.Regs[name] = nil
		}
	}
	if len(q.Marks) > 0 {
		r.Marks = map[rune]*vimtest.Pos{}
	}
	for _, name := range q.Marks {
		if p, ok := t.e.Mark(name); ok {
			r.Marks[name] = &vimtest.Pos{Line: p.Line, Col: p.Col}
		} else {
			r.Marks[name] = nil
		}
	}
	if q.Jumps {
		r.Jumps = []vimtest.Jump{}
		cur := t.e.BufferNum()
		for _, j := range t.e.Jumps() {
			b := j.Buf
			if b == cur {
				b = 0
			}
			r.Jumps = append(r.Jumps, vimtest.Jump{Pos: vimtest.Pos{Line: j.Line, Col: j.Col}, Buf: b})
		}
	}
	if q.Win {
		r.Win = vimtest.Win{Cur: t.e.Window(), Count: t.e.Windows()}
	}
	if q.Top {
		r.Top = t.e.Top() + 1
	}
	if q.Left {
		r.Left = t.e.Left()
	}
	if q.Bufs {
		r.Bufs = t.e.Buffers()
	}
	if q.Msg {
		r.Msg = nvimtext.Notice(t.e.LastNotice())
	}
	return r, nil
}

func (t *testEngine) Close() error { return nil }
