package term

import (
	"bytes"
	"net/url"
	"strconv"
	"strings"
)

// Mark is one OSC 133 shell-integration mark: Kind is its letter ('A' prompt, 'B' input, 'C' output, 'D' done).
type Mark struct {
	Kind     byte
	ExitCode int
	HasExit  bool
}

var osc133 = []byte("\x1b]133;")

// MarkScanner finds OSC 133 marks in a stream of writes, which may split a sequence anywhere.
type MarkScanner struct {
	pend    []byte // the end of the last write, if it began osc133
	in      bool   // inside an OSC 133 sequence
	esc     bool   // its last byte was ESC, maybe starting ST
	payload []byte
}

// maxPayload bounds what is kept of a mark's parameters; the rest is ignored.
const maxPayload = 256

// Next returns how much of p to write before acting on a mark: up to and including the end of the next OSC 133
// sequence, with that mark and ok, or all of p.
func (s *MarkScanner) Next(p []byte) (n int, m Mark, ok bool) {
	i := 0
	if !s.in {
		start, found := s.find(p)
		if !found {
			return len(p), Mark{}, false
		}
		s.in, s.esc, s.payload = true, false, s.payload[:0]
		i = start
	}
	for ; i < len(p); i++ {
		c := p[i]
		switch {
		case c == 0x07, s.esc && c == '\\':
			s.in = false
			m, ok := parseMark(s.payload)
			if !ok {
				return i + 1, Mark{}, false
			}
			return i + 1, m, true
		case c == 0x18, c == 0x1a, s.esc: // CAN, SUB or another ESC sequence cancel the OSC
			s.in = false
			return i, Mark{}, false
		case c == 0x1b:
			s.esc = true
		default:
			if len(s.payload) < maxPayload {
				s.payload = append(s.payload, c)
			}
		}
	}
	return len(p), Mark{}, false
}

// find returns where an OSC 133 sequence's parameters start in p, remembering a start split off p's end.
func (s *MarkScanner) find(p []byte) (int, bool) {
	if len(s.pend) > 0 {
		need := len(osc133) - len(s.pend)
		k := min(need, len(p))
		if bytes.Equal(p[:k], osc133[len(s.pend):len(s.pend)+k]) {
			if k == need {
				s.pend = s.pend[:0]
				return k, true
			}
			s.pend = append(s.pend, p...)
			return 0, false
		}
		s.pend = s.pend[:0]
	}
	if i := bytes.Index(p, osc133); i >= 0 {
		return i + len(osc133), true
	}
	for k := min(len(osc133)-1, len(p)); k > 0; k-- {
		if bytes.HasSuffix(p, osc133[:k]) {
			s.pend = append(s.pend[:0], osc133[:k]...)
			break
		}
	}
	return 0, false
}

// Pending is the start of a mark the scanner has seen but not its end; feeding it to a new scanner's Next puts that
// scanner in the same state.
func (s *MarkScanner) Pending() []byte {
	if !s.in {
		return append([]byte(nil), s.pend...)
	}
	b := append(append([]byte(nil), osc133...), s.payload...)
	if s.esc {
		b = append(b, 0x1b)
	}
	return b
}

func parseMark(p []byte) (Mark, bool) {
	if len(p) == 0 || (len(p) > 1 && p[1] != ';') {
		return Mark{}, false
	}
	m := Mark{Kind: p[0]}
	if m.Kind == 'D' && len(p) > 2 {
		code, _, _ := strings.Cut(string(p[2:]), ";")
		if n, err := strconv.Atoi(code); err == nil {
			m.ExitCode, m.HasExit = n, true
		}
	}
	return m, true
}

// Anchor is a place on the screen an emulator keeps as the text scrolls, reflows and is trimmed.
type Anchor interface {
	Point() (Point, bool) // false once the text is gone
	Free()
}

// CommandLog keeps the commands a shell marks, for an emulator to report as Commands.
type CommandLog struct {
	Max  int // commands kept; the oldest go first. 0 is 1000.
	cmds []logged
}

type logged struct {
	marks    [4]Anchor // A, B, C, D
	finished bool
	exit     int
	hasExit  bool
}

// Mark records m at a, which the log now owns.
func (l *CommandLog) Mark(m Mark, a Anchor) {
	slot := strings.IndexByte("ABCD", m.Kind)
	if slot < 0 {
		a.Free()
		return
	}
	if slot == 0 || len(l.cmds) == 0 || l.last().marks[slot] != nil || l.last().finished {
		l.cmds = append(l.cmds, logged{})
		if max := l.max(); len(l.cmds) > max {
			l.cmds[0].free()
			l.cmds = append(l.cmds[:0], l.cmds[1:]...)
		}
	}
	c := l.last()
	c.marks[slot] = a
	if m.Kind == 'D' {
		c.finished, c.exit, c.hasExit = true, m.ExitCode, m.HasExit
	}
}

func (l *CommandLog) max() int {
	if l.Max > 0 {
		return l.Max
	}
	return 1000
}

func (l *CommandLog) last() *logged { return &l.cmds[len(l.cmds)-1] }

func (c *logged) free() {
	for _, a := range c.marks {
		if a != nil {
			a.Free()
		}
	}
}

// Commands are the logged commands, oldest first; commands whose marks have all scrolled away are dropped.
func (l *CommandLog) Commands() []Command {
	out := make([]Command, 0, len(l.cmds))
	kept := l.cmds[:0]
	for _, c := range l.cmds {
		cmd := Command{Finished: c.finished, ExitCode: c.exit, HasExit: c.hasExit}
		pts := [4]*Point{&cmd.Prompt, &cmd.Input, &cmd.Output, &cmd.End}
		live := false
		for i, a := range c.marks {
			*pts[i] = Point{-1, -1}
			if a == nil {
				continue
			}
			if p, ok := a.Point(); ok {
				*pts[i] = p
				live = true
			}
		}
		if !live {
			c.free()
			continue
		}
		kept = append(kept, c)
		out = append(out, cmd)
	}
	clear(l.cmds[len(kept):])
	l.cmds = kept
	return out
}

// SavedCommand is a logged command as a snapshot keeps it: its marks' points, nil for a mark not sent or gone.
type SavedCommand struct {
	Marks    [4]*Point `json:"marks"`
	Finished bool      `json:"finished,omitempty"`
	ExitCode int       `json:"exit,omitempty"`
	HasExit  bool      `json:"hasExit,omitempty"`
}

// Save is the log as a snapshot keeps it.
func (l *CommandLog) Save() []SavedCommand {
	out := make([]SavedCommand, 0, len(l.cmds))
	for _, c := range l.cmds {
		s := SavedCommand{Finished: c.finished, ExitCode: c.exit, HasExit: c.hasExit}
		for i, a := range c.marks {
			if a == nil {
				continue
			}
			if p, ok := a.Point(); ok {
				s.Marks[i] = &p
			}
		}
		out = append(out, s)
	}
	return out
}

// Load replaces the log with saved commands, anchoring their marks with anchor.
func (l *CommandLog) Load(saved []SavedCommand, anchor func(Point) Anchor) {
	l.Reset()
	for _, s := range saved {
		c := logged{finished: s.Finished, exit: s.ExitCode, hasExit: s.HasExit}
		for i, p := range s.Marks {
			if p != nil {
				c.marks[i] = anchor(*p)
			}
		}
		l.cmds = append(l.cmds, c)
	}
}

// Reset forgets every command.
func (l *CommandLog) Reset() {
	for i := range l.cmds {
		l.cmds[i].free()
	}
	l.cmds = nil
}

// CwdFromURL is the path in an OSC 7 file:// URL, or s itself if it isn't one (OSC 9 and 1337 send a bare path).
func CwdFromURL(s string) string {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "file" {
		return s
	}
	return u.Path
}

// LastOutput is the text the last finished command printed, between its C and D marks.
func LastOutput(e Emulator) string {
	cmds := e.Commands()
	for i := len(cmds) - 1; i >= 0; i-- {
		c := cmds[i]
		if !c.Finished || c.Output.Y < 0 || c.End.Y < 0 {
			continue
		}
		return OutputText(e, c)
	}
	return ""
}

// OutputText is the text a command printed, between its C and D marks.
func OutputText(e Emulator, c Command) string {
	cols, _ := e.Size()
	end := Point{c.End.X - 1, c.End.Y}
	if c.End.X == 0 {
		end = Point{cols - 1, c.End.Y - 1}
	}
	if end.Y < c.Output.Y || (end.Y == c.Output.Y && end.X < c.Output.X) {
		return ""
	}
	return e.Text(c.Output, end)
}
