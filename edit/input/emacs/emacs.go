// Package emacs is a sample input engine written as a plugin would write one: emacs's movement, the mark and
// region, a kill ring and the C-x prefix, on edit's primitives. It isn't built in; Plugin registers it.
package emacs

import (
	"context"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/text"
)

// Engine is the emacs engine.
type Engine struct{}

func (Engine) ID() string { return "emacs" }

func (Engine) Attach(ed *edit.Editor) input.State { return &state{ed: ed, want: -1} }

// Plugin registers the engine, so the input option can pick "emacs".
type Plugin struct{ d interface{ Dispose() } }

func (p *Plugin) Manifest() ext.Manifest { return ext.Manifest{ID: "sample.emacs"} }

func (p *Plugin) Activate(_ context.Context, h ext.Host) error {
	p.d = h.Input(Engine{})
	return nil
}

func (p *Plugin) Deactivate(context.Context) error {
	p.d.Dispose()
	return nil
}

type state struct {
	ed      *edit.Editor
	mark    *text.AnchorID
	kills   []string // the kill ring, newest last
	prefix  string   // "C-x" while a prefix waits for its second key
	lastCmd string
	group   text.UndoGroup
	want    int
}

func (s *state) Mode() input.Mode {
	return input.Mode{Label: "EMACS", Shape: input.Bar, KeyMode: "emacs"}
}

func (s *state) Pending() string { return s.prefix }

func (s *state) cursor() int { return s.ed.Cursors()[0].Off }

func (s *state) goTo(off int) {
	c := edit.Cursor{Off: off}
	if s.mark != nil {
		if m, ok := s.ed.Resolve(*s.mark); ok {
			c.Anchor, c.Selecting = m, true
		}
	}
	s.ed.SetCursors([]edit.Cursor{c})
}

func (s *state) move(motion string) {
	snap := s.ed.Buf()
	vertical := motion == edit.LineNext || motion == edit.LinePrev
	if !vertical || s.want < 0 {
		s.want = edit.RuneCol(snap, s.cursor())
	}
	off, _ := edit.Motion(snap, s.cursor(), motion, 1, s.want)
	s.goTo(off)
	if !vertical {
		s.want = -1
	}
}

// kill removes [from, to) into the kill ring; kills in a row join into one entry, as emacs's do.
func (s *state) kill(from, to int, save bool) {
	if from > to {
		from, to = to, from
	}
	t := string(s.ed.Buf().Slice(text.Range{Start: from, End: to}))
	if s.lastCmd == "kill" && len(s.kills) > 0 {
		s.kills[len(s.kills)-1] += t
	} else {
		s.kills = append(s.kills, t)
	}
	s.ed.SetRegister('+', edit.Register{Text: s.kills[len(s.kills)-1]})
	if !save {
		s.ed.Do([]text.Replace{{Range: text.Range{Start: from, End: to}}}, s.ed.NewGroup())
		s.ed.SetCursors([]edit.Cursor{{Off: from}})
	}
}

func (s *state) region() (int, int, bool) {
	if s.mark == nil {
		return 0, 0, false
	}
	m, ok := s.ed.Resolve(*s.mark)
	return m, s.cursor(), ok
}

func (s *state) clearMark() {
	if s.mark != nil {
		s.ed.Release(*s.mark)
		s.mark = nil
	}
	s.goTo(s.cursor())
}

func (s *state) Key(k string) input.Result {
	cmd := ""
	if s.prefix == "C-x" {
		s.prefix = ""
		switch k {
		case "<C-s>":
			return input.Result{Handled: true, Command: "editor.save"}
		case "u":
			k = "<C-/>"
		case "h":
			k = "<C-x>h"
		default:
			return input.Result{Handled: true}
		}
	}
	defer func() { s.lastCmd = cmd }()
	switch k {
	case "<C-x>":
		s.prefix = "C-x"
	case "<C-f>", "<Right>":
		s.move(edit.CharNext)
	case "<C-b>", "<Left>":
		s.move(edit.CharPrev)
	case "<C-n>", "<Down>":
		s.move(edit.LineNext)
	case "<C-p>", "<Up>":
		s.move(edit.LinePrev)
	case "<C-a>":
		s.move(edit.LineStart)
	case "<C-e>":
		s.move(edit.LineEnd)
	case "<M-f>":
		s.move(edit.WordNext)
	case "<M-b>":
		s.move(edit.WordPrev)
	case "<M-lt>":
		s.move(edit.BufStart)
	case "<M->>":
		s.move(edit.BufEnd)
	case "<C-x>h":
		s.ed.SetCursors([]edit.Cursor{{Off: 0}})
		a := s.ed.Anchor(0, text.Left)
		s.mark = &a
		s.goTo(s.ed.Buf().Len())
	case "<C-Space>", "<C-@>":
		if s.mark != nil {
			s.ed.Release(*s.mark)
		}
		a := s.ed.Anchor(s.cursor(), text.Left)
		s.mark = &a
		s.goTo(s.cursor())
	case "<C-g>":
		s.clearMark()
	case "<C-k>":
		// To the end of the line; at the end, the line break.
		snap := s.ed.Buf()
		from := s.cursor()
		to, _ := edit.Motion(snap, from, edit.LineEnd, 1, 0)
		if to == from && from < snap.Len() {
			to++
		}
		s.kill(from, to, false)
		cmd = "kill"
	case "<C-w>", "<M-w>":
		if from, to, ok := s.region(); ok {
			s.kill(from, to, k == "<M-w>")
			s.clearMark()
		}
		cmd = "kill"
	case "<C-y>":
		if len(s.kills) > 0 {
			s.insert(s.kills[len(s.kills)-1], true)
		} else if r, ok := s.ed.Register('+'); ok {
			s.insert(r.Text, true)
		}
	case "<C-d>", "<Del>":
		snap := s.ed.Buf()
		if from := s.cursor(); from < snap.Len() {
			to, _ := edit.Motion(snap, from, edit.CharNext, 1, 0)
			s.ed.Do([]text.Replace{{Range: text.Range{Start: from, End: to}}}, s.ed.NewGroup())
		}
	case "<BS>":
		if to := s.cursor(); to > 0 {
			from, _ := edit.Motion(s.ed.Buf(), to, edit.CharPrev, 1, 0)
			s.ed.Do([]text.Replace{{Range: text.Range{Start: from, End: to}}}, s.ed.NewGroup())
			s.ed.SetCursors([]edit.Cursor{{Off: from}})
		}
	case "<C-/>", "<C-_>":
		if ch, ok := s.ed.Undo(); ok && len(ch.Replaces) > 0 {
			r := ch.Replaces[len(ch.Replaces)-1]
			s.ed.SetCursors([]edit.Cursor{{Off: r.Range.Start + len(r.Text)}})
		}
	case "<CR>", "<C-m>":
		s.insert("\n", false)
	case "<Tab>":
		s.insert("\t", false)
	case " ":
		s.insert(" ", false)
	default:
		if utf8.RuneCountInString(k) != 1 {
			return input.Result{}
		}
		s.insert(k, false)
		cmd = "self-insert"
	}
	if cmd != "self-insert" {
		s.group = 0
	}
	return input.Result{Handled: true}
}

// insert types t at the point; typed characters undo together, as emacs amalgamates them.
func (s *state) insert(t string, alone bool) {
	if s.group == 0 || alone {
		s.group = s.ed.NewGroup()
	}
	off := s.cursor()
	s.ed.Do([]text.Replace{{Range: text.Range{Start: off, End: off}, Text: []byte(t)}}, s.group)
	s.goTo(off + len(t))
	if alone {
		s.group = 0
	}
	s.want = -1
}
