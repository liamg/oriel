package tour

import (
	"strconv"
	"strings"
)

// BlockKind is what a piece of a step is.
type BlockKind string

const (
	Prose   BlockKind = "prose"   // markdown outside every fence
	Quote   BlockKind = "quote"   // ```go repo=X path=P lines=A-B: code read from a worktree
	Example BlockKind = "example" // ```go: illustrative code from nowhere
	Note    BlockKind = "note"    // ```note 409: pinned under a line of the quote above
	Diagram BlockKind = "diagram" // ```mermaid
	Run     BlockKind = "run"     // ```run: a recorded command, written by tour_run
	Shot    BlockKind = "shot"    // ```shot src=assets/x.png: a screenshot with things pointed at
)

// Block is one piece of a step as written: a fence's info line read into its kind, language, bare argument and
// key=value attributes, and its body (a prose block's text).
type Block struct {
	Kind  BlockKind
	Lang  string
	Body  string
	Arg   string
	Attrs map[string]string
}

func (b Block) intAttr(k string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(b.Attrs[k]))
	return n
}

// Parse splits a step into blocks. Everything outside a fence is prose, one block per run of lines; an unterminated
// fence runs to the end of the file rather than losing it.
func Parse(src string) []Block {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out []Block
	var prose []string
	flush := func() {
		text := strings.Trim(strings.Join(prose, "\n"), "\n")
		prose = nil
		if strings.TrimSpace(text) != "" {
			out = append(out, Block{Kind: Prose, Body: text})
		}
	}
	for i := 0; i < len(lines); i++ {
		open, info := fenceOpen(lines[i])
		if open == "" {
			prose = append(prose, lines[i])
			continue
		}
		var body []string
		for i++; i < len(lines) && !isFenceClose(lines[i], open); i++ {
			body = append(body, lines[i])
		}
		flush()
		out = append(out, infoBlock(info, strings.Join(body, "\n")))
	}
	flush()
	return out
}

func fenceOpen(line string) (marker, info string) {
	t := strings.TrimLeft(line, " ")
	n := 0
	for n < len(t) && t[n] == '`' {
		n++
	}
	if n < 3 {
		return "", ""
	}
	return t[:n], strings.TrimSpace(t[n:])
}

func isFenceClose(line, marker string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, marker) && strings.Trim(t, "`") == ""
}

// infoBlock reads a fence's info line: a first word, an optional bare argument, then key=value pairs whose values
// may be quoted. A code fence that names a repo and a path is a quote; any other is an example.
func infoBlock(info, body string) Block {
	b := Block{Body: body, Attrs: map[string]string{}}
	fields := splitInfo(info)
	if len(fields) == 0 {
		b.Kind = Example
		return b
	}
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			if b.Arg == "" {
				b.Arg = f
			}
			continue
		}
		b.Attrs[strings.ToLower(strings.TrimSpace(k))] = unquote(v)
	}
	switch head := strings.ToLower(fields[0]); head {
	case "note":
		b.Kind = Note
	case "mermaid", "diagram":
		b.Kind, b.Lang = Diagram, "mermaid"
	case "run":
		b.Kind = Run
	case "shot", "screenshot":
		b.Kind = Shot
	default:
		b.Kind, b.Lang = Example, fields[0]
		if b.Attrs["repo"] != "" && b.Attrs["path"] != "" {
			b.Kind = Quote
		}
	}
	return b
}

// splitInfo splits an info line on spaces, keeping quoted values together.
func splitInfo(s string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteByte(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		return v[1 : len(v)-1]
	}
	return v
}

// LineRange reads "406-412", "406" or "406-" into a start and an end line; end is 0 when the range is open.
func LineRange(s string) (start, end int) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "L"))
	if s == "" {
		return 0, 0
	}
	a, b, ok := strings.Cut(s, "-")
	start, err := strconv.Atoi(strings.TrimSpace(a))
	if err != nil {
		return 0, 0
	}
	if !ok {
		return start, start
	}
	end, err = strconv.Atoi(strings.TrimSpace(b))
	if err != nil {
		return start, 0
	}
	return start, end
}

// MarkSet reads "409,418-420" into the lines it names.
func MarkSet(s string) map[int]bool {
	out := map[int]bool{}
	for part := range strings.SplitSeq(s, ",") {
		start, end := LineRange(part)
		if start == 0 {
			continue
		}
		end = max(end, start)
		for n := start; n <= end && n-start < 5000; n++ {
			out[n] = true
		}
	}
	return out
}
