// Package plain is the plain input engine: CUA keys, for people who don't use vim. Typing inserts, arrows move
// (with Shift to select), ⌘C ⌘X ⌘V use the clipboard and ⌘Z undoes. Ctrl does what ⌘ does, as on Linux.
package plain

import (
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
)

// Engine is the plain engine.
type Engine struct{}

func (Engine) ID() string { return "plain" }

func (Engine) Attach(ed *edit.Editor) input.State { return &state{ed: ed, want: -1} }

type state struct {
	ed    *edit.Editor
	group text.UndoGroup // the typing that undoes as one step
	want  int            // the column up and down aim for; -1 to take it from the cursor
}

func (s *state) Mode() input.Mode {
	return input.Mode{Label: "EDIT", Shape: input.Bar, KeyMode: "plain", Text: true}
}

func (s *state) Pending() string { return "" }

// modifiers splits a key into its modifiers and its name: "<D-S-Left>" is "DS", "Left".
func modifiers(k string) (mods, name string) {
	toks := keymap.Parse(k)
	if len(toks) != 1 {
		return "", k
	}
	t := toks[0]
	if !strings.HasPrefix(t, "<") || !strings.HasSuffix(t, ">") || len(t) < 3 {
		return "", t
	}
	body := t[1 : len(t)-1]
	for len(body) > 2 && body[1] == '-' {
		mods += body[:1]
		body = body[2:]
	}
	return mods, body
}

func (s *state) Key(k string) input.Result {
	mods, name := modifiers(k)
	cmd := strings.ContainsAny(mods, "DC") // ⌘, or Ctrl on Linux
	shift := strings.Contains(mods, "S")
	alt := strings.Contains(mods, "M")
	handled := input.Result{Handled: true}
	switch {
	case cmd && name == "a":
		s.ed.SetCursors([]edit.Cursor{{Off: s.ed.Buf().Len(), Anchor: 0, Selecting: true}})
	case cmd && name == "c":
		s.copy(false)
	case cmd && name == "x":
		s.copy(true)
	case cmd && name == "v":
		if r, ok := s.ed.Register('+'); ok {
			s.insert(r.Text, true)
		}
	case cmd && name == "z" && !shift:
		s.after(s.ed.Undo())
	case cmd && (name == "z" && shift || name == "y"):
		s.after(s.ed.Redo())
	case cmd && name == "s":
		return input.Result{Handled: true, Command: "editor.save"}
	case name == "BS":
		s.delete(edit.CharPrev, cmd, alt)
	case name == "Del":
		s.delete(edit.CharNext, cmd, alt)
	case name == "CR" && mods == "":
		s.insert("\n", false)
	case name == "Tab" && mods == "":
		s.insert("\t", false)
	case name == "Left", name == "Right", name == "Up", name == "Down", name == "Home", name == "End":
		s.move(name, shift, cmd, alt)
	case mods == "" && name == " ":
		s.insert(" ", false)
	case mods == "" && utf8.RuneCountInString(name) == 1:
		s.insert(name, false)
	default:
		return input.Result{}
	}
	return handled
}

func (s *state) cursor() edit.Cursor {
	if cs := s.ed.Cursors(); len(cs) > 0 {
		return cs[0]
	}
	return edit.Cursor{}
}

// insert types text over the selection. Typed characters undo together until the cursor moves; a paste is a
// step of its own.
func (s *state) insert(t string, alone bool) {
	if s.group == 0 || alone {
		s.group = s.ed.NewGroup()
	}
	s.ed.Replace(t, s.group)
	if alone {
		s.group = 0
	}
	s.want = -1
}

// delete removes the selection, or what's between the cursor and where motion goes: a word with ⌥, to the start
// of the line with ⌘.
func (s *state) delete(motion string, cmd, alt bool) {
	c := s.cursor()
	if _, ok := c.Selection(); !ok {
		snap := s.ed.Buf()
		switch {
		case cmd && motion == edit.CharPrev:
			motion = edit.LineStart
		case cmd:
			motion = edit.LineEnd
		case alt && motion == edit.CharPrev:
			motion = edit.WordPrev
		case alt:
			motion = edit.WordNext
		}
		to, _ := edit.Motion(snap, c.Off, motion, 1, 0)
		if to == c.Off {
			return
		}
		s.ed.SetCursors([]edit.Cursor{{Off: to, Anchor: c.Off, Selecting: true}})
	}
	s.insert("", false)
}

// move is the arrows, Home and End: with ⌥ by word, with ⌘ to the line's (or buffer's) ends, with Shift selecting.
func (s *state) move(key string, shift, cmd, alt bool) {
	c := s.cursor()
	snap := s.ed.Buf()
	if r, ok := c.Selection(); ok && !shift && (key == "Left" || key == "Right") && !cmd && !alt {
		off := r.Start
		if key == "Right" {
			off = r.End
		}
		s.ed.SetCursors([]edit.Cursor{{Off: off}})
		s.group, s.want = 0, -1
		return
	}
	motion := map[string]string{"Left": edit.CharPrev, "Right": edit.CharNext, "Up": edit.LinePrev, "Down": edit.LineNext,
		"Home": edit.LineStart, "End": edit.LineEnd}[key]
	switch {
	case cmd && key == "Left":
		motion = edit.LineStart
	case cmd && key == "Right":
		motion = edit.LineEnd
	case cmd && key == "Up":
		motion = edit.BufStart
	case cmd && key == "Down":
		motion = edit.BufEnd
	case alt && key == "Left":
		motion = edit.WordPrev
	case alt && key == "Right":
		motion = edit.WordNext
	}
	vertical := motion == edit.LinePrev || motion == edit.LineNext
	if !vertical || s.want < 0 {
		s.want = edit.RuneCol(snap, c.Off)
	}
	to, _ := edit.Motion(snap, c.Off, motion, 1, s.want)
	next := edit.Cursor{Off: to}
	if shift {
		next.Anchor, next.Selecting = c.Off, true
		if c.Selecting {
			next.Anchor = c.Anchor
		}
	}
	s.ed.SetCursors([]edit.Cursor{next})
	s.group = 0
	if !vertical {
		s.want = -1
	}
}

// copy puts the selection on the clipboard, or the whole line when nothing's selected; cut deletes it too.
func (s *state) copy(cut bool) {
	c := s.cursor()
	snap := s.ed.Buf()
	r, ok := c.Selection()
	kind := edit.Charwise
	if !ok {
		p := snap.PosAt(c.Off)
		r = text.Range{Start: snap.LineStart(p.Line), End: min(snap.LineStart(p.Line)+len(snap.Line(p.Line))+1, snap.Len())}
		kind = edit.Linewise
	}
	t := string(snap.Slice(r))
	if kind == edit.Linewise && !strings.HasSuffix(t, "\n") {
		t += "\n"
	}
	s.ed.SetRegister('+', edit.Register{Text: t, Kind: kind})
	if cut {
		s.ed.SetCursors([]edit.Cursor{{Off: r.End, Anchor: r.Start, Selecting: true}})
		s.insert("", true)
	}
}

// after puts the cursor where an undo or redo changed the text.
func (s *state) after(ch text.Change, ok bool) {
	s.group, s.want = 0, -1
	if !ok || len(ch.Replaces) == 0 {
		return
	}
	r := ch.Replaces[len(ch.Replaces)-1]
	s.ed.SetCursors([]edit.Cursor{{Off: min(r.Range.Start+len(r.Text), s.ed.Buf().Len())}})
}
