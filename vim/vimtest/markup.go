package vimtest

import (
	"fmt"
	"strings"
)

// Pos is a buffer position: 0-based line and 0-based byte column.
type Pos struct {
	Line, Col int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line+1, p.Col) }

// Jump is a jumplist entry: a position, and its buffer's number when that isn't the current buffer (0 when it is).
type Jump struct {
	Pos
	Buf int
}

func (j Jump) String() string {
	if j.Buf == 0 {
		return j.Pos.String()
	}
	return fmt.Sprintf("%s@%d", j.Pos, j.Buf)
}

func (p Pos) less(q Pos) bool {
	return p.Line < q.Line || p.Line == q.Line && p.Col < q.Col
}

// Buffer is a parsed buffer markup: text, cursor, and an optional charwise visual selection.
//
// Markup: `|` sits before the character under the cursor; `[` and `]` enclose a visual
// selection; `^` marks the visual start when it matters. `\` escapes any of `|[]^\`.
type Buffer struct {
	Lines  []string
	Cursor Pos
	// Visual is set when the markup holds a selection. Start and End are inclusive, in order.
	Visual *Visual
}

// Visual is a charwise selection. Anchor is the end opposite the cursor (vim's "v" position).
type Visual struct {
	Start, End Pos
	Anchor     Pos
	// AnchorSet is true when the markup gave the anchor with `^`.
	AnchorSet bool
}

// ParseBuffer parses buffer markup. Lines are separated by "\n".
func ParseBuffer(s string) (Buffer, error) {
	var b Buffer
	var cursor, start, end, anchor *Pos
	for li, raw := range strings.Split(s, "\n") {
		var line []byte
		for i := 0; i < len(raw); i++ {
			here := Pos{li, len(line)}
			switch c := raw[i]; c {
			case '\\':
				if i+1 < len(raw) && strings.IndexByte(`|[]^\`, raw[i+1]) >= 0 {
					i++
					line = append(line, raw[i])
				} else {
					line = append(line, c)
				}
			case '|':
				if cursor != nil {
					return b, fmt.Errorf("two cursors in %q", s)
				}
				cursor = &here
			case '[':
				if start != nil {
					return b, fmt.Errorf("two '[' in %q", s)
				}
				start = &here
			case ']':
				if end != nil {
					return b, fmt.Errorf("two ']' in %q", s)
				}
				end = &here
			case '^':
				if anchor != nil {
					return b, fmt.Errorf("two '^' in %q", s)
				}
				anchor = &here
			default:
				line = append(line, c)
			}
		}
		b.Lines = append(b.Lines, string(line))
	}
	if (start == nil) != (end == nil) {
		return b, fmt.Errorf("unbalanced visual markup in %q", s)
	}
	if start == nil {
		if anchor != nil {
			return b, fmt.Errorf("'^' outside a selection in %q", s)
		}
		if cursor == nil {
			return b, fmt.Errorf("no cursor '|' in %q", s)
		}
		b.Cursor = *cursor
		return b, nil
	}
	last, ok := b.before(*end)
	if !ok || end.less(*start) || !start.less(*end) {
		return b, fmt.Errorf("empty or reversed selection in %q", s)
	}
	v := &Visual{Start: *start, End: last}
	switch {
	case cursor != nil:
		b.Cursor = *cursor
	case anchor != nil && *anchor == last:
		b.Cursor = v.Start
	default:
		b.Cursor = last
	}
	if anchor != nil {
		v.Anchor, v.AnchorSet = *anchor, true
	} else if b.Cursor == v.Start {
		v.Anchor = last
	} else {
		v.Anchor = v.Start
	}
	b.Visual = v
	return b, nil
}

// before returns the position of the character just before p (the last selected one when p
// is a closing `]`), stepping back over a line break.
func (b Buffer) before(p Pos) (Pos, bool) {
	if p.Col > 0 {
		line := b.Lines[p.Line]
		i := p.Col - 1
		for i > 0 && line[i]&0xc0 == 0x80 {
			i--
		}
		return Pos{p.Line, i}, true
	}
	if p.Line == 0 {
		return p, false
	}
	// A selection ending at a line break includes the previous line's end.
	return Pos{p.Line - 1, len(b.Lines[p.Line-1])}, true
}

// String renders the buffer back as markup.
func (b Buffer) String() string {
	var sb strings.Builder
	endAt := Pos{-1, -1}
	if v := b.Visual; v != nil {
		if line := b.Lines[v.End.Line]; v.End.Col < len(line) {
			endAt = Pos{v.End.Line, v.End.Col + charLen(line[v.End.Col:])}
		} else {
			endAt = Pos{v.End.Line + 1, 0}
		}
	}
	for li, line := range b.Lines {
		if li > 0 {
			sb.WriteByte('\n')
		}
		for col := 0; col <= len(line); col++ {
			p := Pos{li, col}
			if p == endAt {
				sb.WriteByte(']')
			}
			if b.Visual != nil && p == b.Visual.Start {
				sb.WriteByte('[')
			}
			if b.Visual != nil && b.Visual.AnchorSet && p == b.Visual.Anchor {
				sb.WriteByte('^')
			}
			if p == b.Cursor {
				sb.WriteByte('|')
			}
			if col < len(line) {
				if strings.IndexByte(`|[]^\`, line[col]) >= 0 {
					sb.WriteByte('\\')
				}
				sb.WriteByte(line[col])
			}
		}
	}
	if endAt.Line == len(b.Lines) {
		sb.WriteByte(']')
	}
	return sb.String()
}

func charLen(s string) int {
	n := 1
	for n < len(s) && s[n]&0xc0 == 0x80 {
		n++
	}
	return n
}
