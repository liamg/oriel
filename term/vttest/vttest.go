// Package vttest runs esctest-style cases against a term.Emulator: bytes in, expected screen and state out.
//
// A case file holds cases like this one; fields other than the name, ref, size and input are checks, and a case
// checks only what it lists:
//
//	=== CUP moves to row;column
//	ref:    xterm/ctlseqs.txt CSI Ps ; Ps H
//	quote:  "Cursor Position [row;column] (default = [1,1]) (CUP)."
//	size:   10x3
//	setup:  "written, then a Grid taken, before input"
//	input:  "abc\x1b[2;5Hx"
//	screen:
//	  |abc       |
//	  |    x     |
//	  |          |
//	cursor: 5,1
//
// Strings are Go-quoted, and several input lines are joined. Checks:
//
//	screen   the viewport's lines between bars (a wide character takes one place, its second cell none)
//	history  the scrollback's lines above the viewport, trailing spaces trimmed, oldest first
//	cursor   x,y in the active area
//	shape    block, bar, underline or hollow
//	title    the window title
//	cwd      the directory from OSC 7
//	alt      true while the alternate screen shows
//	mode     "2026 on", "ansi 4 off": a DEC private (or ANSI) mode's state; may repeat
//	held     true while synchronized output holds the frame
//	cell     "x,y fg=1 bg=rgb:1,2,3 ul=5 bold italic faint inverse strike underline=curly width=wide text=\"é\"": the
//	         listed properties of one cell, colours as default, a palette index or rgb:r,g,b; may repeat
//	dirty    the viewport rows the Grid after input marks dirty, as numbers
//	reply    what the terminal answered, for the PTY
//	commands one line per OSC 133 command: "A=x,y B=x,y C=x,y D=x,y exit=n", a missing mark as -
//	output   the last finished command's output
package vttest

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/term"
)

// Case is one case.
type Case struct {
	Name, File string
	Line       int
	Ref, Quote string
	Cols, Rows int
	Setup      string
	Input      string
	Checks     []Check
}

// Check is one expectation: a field and its value, with multi-line values joined by newlines.
type Check struct {
	Field, Value string
	Line         int
}

var checks = []string{"screen", "history", "cursor", "shape", "title", "cwd", "alt", "mode", "held", "cell", "dirty", "reply", "commands", "output"}

// Load reads every case in the files matching glob.
func Load(t testing.TB, glob string) []Case {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		t.Fatalf("no case files match %s", glob)
	}
	var out []Case
	for _, f := range files {
		cs, err := Parse(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, cs...)
	}
	return out
}

// Parse reads the cases in one file.
func Parse(path string) ([]Case, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var (
		out   []Case
		c     *Case
		multi *Check // a field whose value is the indented lines that follow
	)
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if multi != nil {
			if v, ok := strings.CutPrefix(line, "  "); ok {
				if multi.Value != "" {
					multi.Value += "\n"
				}
				multi.Value += v
				continue
			}
			c.Checks = append(c.Checks, *multi)
			multi = nil
		}
		if name, ok := strings.CutPrefix(line, "=== "); ok {
			out = append(out, Case{Name: strings.TrimSpace(name), File: path, Line: n})
			c = &out[len(out)-1]
			continue
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if c == nil {
			return nil, fmt.Errorf("%s:%d: text before the first case", path, n)
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("%s:%d: want field: value", path, n)
		}
		value = strings.TrimSpace(value)
		switch field {
		case "ref":
			c.Ref = value
		case "quote":
			q, err := unquote(value)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, n, err)
			}
			c.Quote = q
		case "size":
			if _, err := fmt.Sscanf(value, "%dx%d", &c.Cols, &c.Rows); err != nil {
				return nil, fmt.Errorf("%s:%d: size: %v", path, n, err)
			}
		case "setup", "input":
			s, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %s: %v", path, n, field, err)
			}
			if field == "setup" {
				c.Setup += s
			} else {
				c.Input += s
			}
		default:
			if !slices.Contains(checks, field) {
				return nil, fmt.Errorf("%s:%d: unknown field %q", path, n, field)
			}
			ch := Check{Field: field, Value: value, Line: n}
			if value == "" {
				multi = &ch
				continue
			}
			c.Checks = append(c.Checks, ch)
		}
	}
	if multi != nil {
		c.Checks = append(c.Checks, *multi)
	}
	return out, sc.Err()
}

func unquote(s string) (string, error) {
	if !strings.HasPrefix(s, `"`) {
		return s, nil
	}
	return strconv.Unquote(s)
}

// Run runs the case on a new emulator from mk and reports every check that fails.
func (c Case) Run(t *testing.T, mk func(cols, rows int, reply func([]byte)) term.Emulator) {
	t.Helper()
	if c.Ref == "" || c.Quote == "" {
		t.Fatalf("%s:%d: every case cites a source with a ref and a quote (docs/architecture.md, \"Testing\")", c.File, c.Line)
	}
	if c.Cols == 0 {
		t.Fatalf("%s:%d: no size", c.File, c.Line)
	}
	var reply []byte
	e := mk(c.Cols, c.Rows, func(p []byte) { reply = append(reply, p...) })
	defer e.Close()
	if c.Setup != "" {
		e.Write([]byte(c.Setup))
		e.Grid()
	}
	e.Write([]byte(c.Input))
	g := e.Grid()
	for _, ch := range c.Checks {
		if err := check(e, g, ch, reply); err != nil {
			t.Errorf("%s:%d: %s: %v", c.File, ch.Line, ch.Field, err)
		}
	}
}

func check(e term.Emulator, g term.Grid, ch Check, reply []byte) error {
	want := ch.Value
	switch ch.Field {
	case "screen":
		var got []string
		for _, l := range g.Lines {
			got = append(got, "|"+l.Text()+"|")
		}
		return diff(strings.Join(got, "\n"), want)
	case "history":
		sb := e.Scrollbar()
		cols, rows := e.Size()
		var got []string
		for y := range sb.Total - rows {
			got = append(got, strings.TrimRight(e.Text(term.Point{Y: y}, term.Point{X: cols - 1, Y: y}), " "))
		}
		return diff(strings.Join(got, "\n"), want)
	case "cursor":
		x, y, _ := e.Cursor()
		return diff(fmt.Sprintf("%d,%d", x, y), want)
	case "shape":
		_, _, s := e.Cursor()
		return diff([]string{"bar", "block", "underline", "hollow"}[s], want)
	case "title":
		return diffQuoted(e.Title(), want)
	case "cwd":
		return diff(e.Cwd(), want)
	case "alt":
		return diff(strconv.FormatBool(e.AltScreen()), want)
	case "held":
		return diff(strconv.FormatBool(e.Held()), want)
	case "mode":
		f := strings.Fields(want)
		m := term.Mode{}
		if len(f) == 3 && f[0] == "ansi" {
			m.ANSI = true
			f = f[1:]
		}
		if len(f) != 2 {
			return fmt.Errorf("want \"N on|off\" or \"ansi N on|off\", got %q", want)
		}
		n, err := strconv.Atoi(f[0])
		if err != nil {
			return err
		}
		m.N = uint16(n)
		return diff(map[bool]string{true: "on", false: "off"}[e.Mode(m)], f[1])
	case "cell":
		return checkCell(g, want)
	case "dirty":
		var got []string
		for i, l := range g.Lines {
			if l.Dirty {
				got = append(got, strconv.Itoa(i))
			}
		}
		return diff(strings.Join(got, " "), want)
	case "reply":
		return diffQuoted(string(reply), want)
	case "commands":
		var got []string
		for _, c := range e.Commands() {
			s := fmt.Sprintf("A=%s B=%s C=%s D=%s", pt(c.Prompt), pt(c.Input), pt(c.Output), pt(c.End))
			if c.HasExit {
				s += fmt.Sprintf(" exit=%d", c.ExitCode)
			}
			got = append(got, s)
		}
		return diff(strings.Join(got, "\n"), want)
	case "output":
		return diffQuoted(term.LastOutput(e), want)
	}
	return fmt.Errorf("unknown check")
}

func pt(p term.Point) string {
	if p.Y < 0 {
		return "-"
	}
	return fmt.Sprintf("%d,%d", p.X, p.Y)
}

func diff(got, want string) error {
	if got != want {
		return fmt.Errorf("\ngot:\n%s\nwant:\n%s", got, want)
	}
	return nil
}

func diffQuoted(got, want string) error {
	w, err := unquote(want)
	if err != nil {
		return err
	}
	if got != w {
		return fmt.Errorf("got %q, want %q", got, w)
	}
	return nil
}

func checkCell(g term.Grid, spec string) error {
	pos, props, _ := strings.Cut(spec, " ")
	var x, y int
	if _, err := fmt.Sscanf(pos, "%d,%d", &x, &y); err != nil {
		return fmt.Errorf("position %q: %v", pos, err)
	}
	if y >= len(g.Lines) || x >= len(g.Lines[y].Cells) {
		return fmt.Errorf("no cell %d,%d", x, y)
	}
	c := g.Lines[y].Cells[x]
	var errs []string
	for _, p := range fields(props) {
		k, v, _ := strings.Cut(p, "=")
		var got, want string
		switch k {
		case "fg", "bg", "ul":
			col := map[string]term.Color{"fg": c.FG, "bg": c.BG, "ul": c.UnderlineColor}[k]
			got, want = colour(col), v
		case "bold", "italic", "faint", "blink", "inverse", "invisible", "strike", "overline":
			a := map[string]term.Attrs{"bold": term.Bold, "italic": term.Italic, "faint": term.Faint, "blink": term.Blink,
				"inverse": term.Inverse, "invisible": term.Invisible, "strike": term.Strikethrough, "overline": term.Overline}[k]
			got, want = strconv.FormatBool(c.Attrs&a != 0), "true"
			if v != "" {
				want = v
			}
		case "underline":
			got, want = []string{"none", "single", "double", "curly", "dotted", "dashed"}[c.Underline], v
		case "width":
			got, want = []string{"narrow", "wide", "tail", "head"}[c.Width], v
		case "text":
			got = c.Text
			w, err := unquote(v)
			if err != nil {
				return err
			}
			want = w
		default:
			return fmt.Errorf("unknown cell property %q", k)
		}
		if got != want {
			errs = append(errs, fmt.Sprintf("%s = %q, want %q", k, got, want))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cell %d,%d: %s", x, y, strings.Join(errs, "; "))
	}
	return nil
}

// fields splits on spaces outside quotes.
func fields(s string) []string {
	var out []string
	var b strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '\\' && quoted && i+1 < len(s):
			b.WriteByte(ch)
			i++
			b.WriteByte(s[i])
		case ch == '"':
			quoted = !quoted
			b.WriteByte(ch)
		case ch == ' ' && !quoted:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteByte(ch)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

func colour(c term.Color) string {
	switch c.Kind() {
	case term.ColorPalette:
		return strconv.Itoa(int(c.Index()))
	case term.ColorRGB:
		r, g, b := c.RGB()
		return fmt.Sprintf("rgb:%d,%d,%d", r, g, b)
	}
	return "default"
}
