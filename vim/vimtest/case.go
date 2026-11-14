// Package vimtest runs vim keystroke cases (.vt files, see docs/architecture.md) against an
// Engine, and checks their expectations against headless nvim in oracle mode.
package vimtest

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Case is one `=== name` block of a .vt file. Optional fields are nil or empty when not declared.
type Case struct {
	Name string
	File string
	Line int

	Ref       string
	Quote     string
	Follows   string // "vim" or "nvim", when the two differ
	Deviation string // why we knowingly differ from the oracle

	Given  Buffer
	Keys   []Key
	Expect Buffer

	Mode    string           // a Mode* constant
	Regs    map[rune]*string // nil value: unset
	Marks   map[rune]*Pos    // nil value: unset
	Jumps   []Jump           // nil: not checked
	Undo    *Buffer          // the buffer after the keys and then `u`
	Dot     *Buffer          // the buffer after the keys and then `.`
	Msg     *string          // the last message
	Win     *Win             // the current window's number and the window count
	Top     *int             // the window's first line, 1-based, as line('w0')
	Left    *int             // the window's first column, 0-based, as winsaveview().leftcol
	Bufs    *string          // the listed buffers, as `1# 2%`
	RawKeys string
}

// Win is the current window's number (1-based, as winnr()) and how many windows the tab page has.
type Win struct{ Cur, Count int }

func (w Win) String() string { return fmt.Sprintf("%d/%d", w.Cur, w.Count) }

// Modes, as written in a case's `mode:` field.
const (
	ModeNormal      = "normal"
	ModeInsert      = "insert"
	ModeReplace     = "replace"
	ModeVisual      = "visual"
	ModeVisualLine  = "visual-line"
	ModeVisualBlock = "visual-block"
	ModeSelect      = "select"
	ModeOpPending   = "op-pending"
	ModeCmdline     = "cmdline"
)

var modes = map[string]bool{
	ModeNormal: true, ModeInsert: true, ModeReplace: true, ModeVisual: true, ModeVisualLine: true,
	ModeVisualBlock: true, ModeSelect: true, ModeOpPending: true, ModeCmdline: true,
}

// Load parses every .vt file matching glob, failing the test on any error.
func Load(t testing.TB, glob string) []Case {
	t.Helper()
	files, err := filepath.Glob(glob)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("vimtest: no files match %s", glob)
	}
	var out []Case
	for _, f := range files {
		r, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		cs, err := Parse(f, r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, cs...)
	}
	return out
}

// Parse reads the cases in a .vt file. Lines starting with '#' outside a value are comments.
// A field whose value is empty takes the following lines indented by two spaces, with the
// indent removed, as a multi-line value. Blank lines at its end aren't part of it; a line of just the
// indent is an empty line, even at the end.
func Parse(file string, r io.Reader) ([]Case, error) {
	var (
		cases  []Case
		cur    *Case
		fields map[string]string
		lines  []string
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	finish := func() error {
		if cur == nil {
			return nil
		}
		if err := cur.fill(fields); err != nil {
			return fmt.Errorf("%s:%d: %s: %w", file, cur.Line, cur.Name, err)
		}
		cases = append(cases, *cur)
		return nil
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "==="):
			if err := finish(); err != nil {
				return nil, err
			}
			cur = &Case{Name: strings.TrimSpace(line[3:]), File: file, Line: i + 1}
			fields = map[string]string{}
		case strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#"):
		case cur == nil:
			return nil, fmt.Errorf("%s:%d: field before the first ===", file, i+1)
		default:
			key, val, ok := strings.Cut(line, ":")
			key = strings.TrimSpace(key)
			if !ok || strings.ContainsAny(key, " \t") {
				return nil, fmt.Errorf("%s:%d: want `field: value`, got %q", file, i+1, line)
			}
			if _, dup := fields[key]; dup {
				return nil, fmt.Errorf("%s:%d: %s given twice", file, i+1, key)
			}
			val = strings.TrimSpace(val)
			if val == "" {
				var block, raw []string
				for i+1 < len(lines) && (strings.HasPrefix(lines[i+1], "  ") || lines[i+1] == "") {
					i++
					block = append(block, strings.TrimPrefix(lines[i], "  "))
					raw = append(raw, lines[i])
				}
				// Blank lines at the end separate cases; an indented empty line is part of the value.
				for len(raw) > 0 && raw[len(raw)-1] == "" {
					block, raw = block[:len(block)-1], raw[:len(raw)-1]
				}
				val = strings.Join(block, "\n")
			}
			fields[key] = val
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	return cases, nil
}

func (c *Case) fill(f map[string]string) error {
	var err error
	take := func(k string) (string, bool) {
		v, ok := f[k]
		delete(f, k)
		return v, ok
	}
	buffer := func(k string) (*Buffer, error) {
		v, ok := take(k)
		if !ok {
			return nil, nil
		}
		b, err := ParseBuffer(expandRanges(v))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		return &b, nil
	}
	c.Ref, _ = take("ref")
	c.Follows, _ = take("follows")
	c.Deviation, _ = take("deviation")
	if q, ok := take("quote"); ok {
		if c.Quote, err = unquote(q); err != nil {
			return fmt.Errorf("quote: %w", err)
		}
	}
	if c.Follows != "" && c.Follows != "vim" && c.Follows != "nvim" {
		return fmt.Errorf("follows: want vim or nvim, got %q", c.Follows)
	}
	given, err := buffer("given")
	if err != nil {
		return err
	}
	expect, err := buffer("expect")
	if err != nil {
		return err
	}
	if given == nil || expect == nil {
		return fmt.Errorf("given and expect are required")
	}
	c.Given, c.Expect = *given, *expect
	keys, ok := take("keys")
	if !ok {
		return fmt.Errorf("keys is required")
	}
	c.RawKeys, c.Keys = keys, ParseKeys(keys)
	if c.Undo, err = buffer("undo"); err != nil {
		return err
	}
	if c.Dot, err = buffer("dot"); err != nil {
		return err
	}
	if m, ok := take("mode"); ok {
		if !modes[m] {
			return fmt.Errorf("mode: unknown mode %q", m)
		}
		c.Mode = m
	}
	if m, ok := take("msg"); ok {
		if m, err = unquote(m); err != nil {
			return fmt.Errorf("msg: %w", err)
		}
		c.Msg = &m
	}
	if w, ok := take("win"); ok {
		cur, count, ok := strings.Cut(w, "/")
		n, err1 := strconv.Atoi(cur)
		m, err2 := strconv.Atoi(count)
		if !ok || err1 != nil || err2 != nil || n < 1 || n > m {
			return fmt.Errorf("win: want current/count, got %q", w)
		}
		c.Win = &Win{n, m}
	}
	if t, ok := take("top"); ok {
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 {
			return fmt.Errorf("top: want a line number, got %q", t)
		}
		c.Top = &n
	}
	if l, ok := take("left"); ok {
		n, err := strconv.Atoi(l)
		if err != nil || n < 0 {
			return fmt.Errorf("left: want a column, got %q", l)
		}
		c.Left = &n
	}
	if b, ok := take("bufs"); ok {
		if c.Bufs, err = parseBufs(b); err != nil {
			return fmt.Errorf("bufs: %w", err)
		}
	}
	if r, ok := take("regs"); ok {
		if c.Regs, err = parseRegs(r); err != nil {
			return fmt.Errorf("regs: %w", err)
		}
	}
	if m, ok := take("marks"); ok {
		if c.Marks, err = parseMarks(m); err != nil {
			return fmt.Errorf("marks: %w", err)
		}
	}
	if j, ok := take("jumps"); ok {
		c.Jumps = []Jump{}
		for _, s := range strings.Fields(j) {
			jp, err := parseJump(s)
			if err != nil {
				return fmt.Errorf("jumps: %w", err)
			}
			c.Jumps = append(c.Jumps, jp)
		}
	}
	for k := range f {
		return fmt.Errorf("unknown field %q", k)
	}
	return nil
}

// unquote reads a Go-quoted string, or takes the value as is when it isn't quoted.
func unquote(s string) (string, error) {
	if strings.HasPrefix(s, `"`) {
		return strconv.Unquote(s)
	}
	return s, nil
}

const unset = "(unset)"

// parseRegs reads `"x = "text"` pairs; `""` is the unnamed register and `(unset)` an empty one.
func parseRegs(s string) (map[rune]*string, error) {
	regs := map[rune]*string{}
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		if len(s) < 2 || s[0] != '"' {
			return nil, fmt.Errorf("want `\"x = value`, at %q", s)
		}
		name := rune(s[1])
		s = strings.TrimSpace(s[2:])
		if !strings.HasPrefix(s, "=") {
			return nil, fmt.Errorf("want '=' after register %q", name)
		}
		s = strings.TrimSpace(s[1:])
		if strings.HasPrefix(s, unset) {
			regs[name] = nil
			s = s[len(unset):]
			continue
		}
		q, err := strconv.QuotedPrefix(s)
		if err != nil {
			return nil, fmt.Errorf("register %q: want a quoted string or %s, at %q", name, unset, s)
		}
		v, _ := strconv.Unquote(q)
		regs[name] = &v
		s = s[len(q):]
	}
	return regs, nil
}

// parseMarks reads `a=1:0 '<=2:3 b=(unset)`; the leading quote on a mark name is optional.
func parseMarks(s string) (map[rune]*Pos, error) {
	marks := map[rune]*Pos{}
	for _, f := range strings.Fields(s) {
		name, val, ok := strings.Cut(strings.TrimPrefix(f, "'"), "=")
		if !ok || len([]rune(name)) != 1 {
			return nil, fmt.Errorf("want name=line:col, got %q", f)
		}
		r := []rune(name)[0]
		if val == unset {
			marks[r] = nil
			continue
		}
		p, err := parsePos(val)
		if err != nil {
			return nil, err
		}
		marks[r] = &p
	}
	return marks, nil
}

// parseBufs reads listed buffer numbers in ascending order, each flagged `%` (current) or `#`
// (alternate) as :ls does, and returns them space-separated.
func parseBufs(s string) (*string, error) {
	fields := strings.Fields(s)
	prev := 0
	for _, f := range fields {
		n, err := strconv.Atoi(strings.TrimRight(f, "%#"))
		if err != nil || n <= prev || len(f)-len(strings.TrimRight(f, "%#")) > 1 {
			return nil, fmt.Errorf("want ascending numbers flagged %% or #, got %q", s)
		}
		prev = n
	}
	out := strings.Join(fields, " ")
	return &out, nil
}

// parseJump reads `line:col`, or `line:col@N` for an entry in buffer N when that isn't the current one.
func parseJump(s string) (Jump, error) {
	at, buf, ok := strings.Cut(s, "@")
	p, err := parsePos(at)
	if err != nil {
		return Jump{}, err
	}
	if !ok {
		return Jump{Pos: p}, nil
	}
	n, err := strconv.Atoi(buf)
	if err != nil || n < 1 {
		return Jump{}, fmt.Errorf("want line:col@buffer, got %q", s)
	}
	return Jump{p, n}, nil
}

// parsePos reads `line:col`: 1-based line, 0-based byte column, as in :marks.
func parsePos(s string) (Pos, error) {
	l, c, ok := strings.Cut(s, ":")
	line, err1 := strconv.Atoi(l)
	col, err2 := strconv.Atoi(c)
	if !ok || err1 != nil || err2 != nil || line < 1 || col < 0 {
		return Pos{}, fmt.Errorf("want line:col, got %q", s)
	}
	return Pos{line - 1, col}, nil
}

// expandRanges replaces each line of a buffer value that is just {a..b} with the lines a to b, each holding
// its own number, so a case can have a long buffer without spelling it out.
func expandRanges(v string) string {
	lines := strings.Split(v, "\n")
	var out []string
	for _, l := range lines {
		var a, b int
		if n, err := fmt.Sscanf(l, "{%d..%d}", &a, &b); err == nil && n == 2 && l == fmt.Sprintf("{%d..%d}", a, b) && a <= b {
			for i := a; i <= b; i++ {
				out = append(out, strconv.Itoa(i))
			}
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}
