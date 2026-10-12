package vimregex

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// buf is test text as Input.
type buf []string

func (b buf) Lines() int        { return len(b) }
func (b buf) Line(n int) []byte { return []byte(b[n]) }

func lines(s string) buf { return strings.Split(s, "\n") }

// vcol counts screen cells with 'tabstop' 8 and double-width CJK, as nvim does by default.
func (b buf) vcol(line, col int) int {
	v := 0
	text := b[line]
	for i := 0; i < col && i < len(text); {
		r, l := decode([]byte(text), i)
		switch {
		case r == '\t':
			v += 8 - v%8
		case r >= 0x1100 && r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf, r >= 0xac00 && r <= 0xd7a3,
			r >= 0xf900 && r <= 0xfaff, r >= 0xff00 && r <= 0xff60:
			v += 2
		default:
			v++
		}
		i += l
	}
	return v
}

// sc is a :s case: the buffer after ":{range}s/{pat}/{rep}/e[g]" with the given options and state.
type sc struct {
	pat     string
	in      string
	want    string
	rep     string // "(&)" when empty
	g       bool
	ic, scs bool
	nomagic bool
	last    string // the previous substitute string: ~ in pat and rep
	line    int    // 1-based line to run on; 0: every line
	search  bool   // instead of :s, where searchpos(pat, 'cnW') from the start finds the first match, as "line:col"
	cur     *Pos
	vis     *Area
	marks   map[rune]Pos
}

func (c sc) replacement() string {
	if c.rep == "" {
		return "(&)"
	}
	return c.rep
}

func (c sc) state(b buf) *State {
	st := &State{Visual: c.vis, VCol: b.vcol}
	if c.cur != nil {
		st.Cursor = *c.cur
	}
	if c.marks != nil {
		st.Mark = func(r rune) (Pos, bool) { p, ok := c.marks[r]; return p, ok }
	}
	return st
}

// run does what vim's :s does with the case, on the text as it was before the command.
func (c sc) run(t *testing.T) string {
	t.Helper()
	re, err := Compile(c.pat, Options{IgnoreCase: c.ic, SmartCase: c.scs, Magic: !c.nomagic, LastSub: c.last})
	if err != nil {
		return "error: " + err.Error()
	}
	b := lines(c.in)
	st := c.state(b)
	if c.search {
		for l := range b {
			if m, ok := re.Exec(b, l, 0, st); ok {
				return fmt.Sprintf("%d:%d", m.Start.Line+1, m.Start.Col+1)
			}
		}
		return "0:0"
	}
	rep := Tilde(c.replacement(), c.last, !c.nomagic)
	multi := re.Multiline()
	type hit struct {
		m    Match
		text string
	}
	var hits []hit
	first, last := 0, len(b)-1
	if c.line > 0 {
		first, last = c.line-1, c.line-1
	}
	for l := first; l <= last && l < len(b); {
		col, prev, next := 0, -1, l+1
		for {
			m, ok, err := re.ExecErr(b, l, col, st)
			if err != nil {
				t.Fatalf("%q: %v", c.pat, err)
			}
			if !ok {
				break
			}
			if m.Start == m.End && col == prev && m.End.Line == l && m.End.Col == col {
				if col >= len(b[l]) {
					break
				}
				col = clusterEnd([]byte(b[l]), col)
				if col >= len(b[l]) && !multi {
					break
				}
				continue
			}
			if m.Start.Line >= len(b) {
				break
			}
			hits = append(hits, hit{m, Expand(rep, m, b, !c.nomagic)})
			if m.End.Line > l {
				if m.End.Line >= len(b) {
					next = len(b)
					break
				}
				l, col, prev, next = m.End.Line, m.End.Col, m.End.Col, m.End.Line+1
				continue
			}
			if !c.g {
				break
			}
			col, prev = m.End.Col, m.End.Col
			if col >= len(b[l]) && !multi {
				break
			}
		}
		l = next
	}
	text := strings.Join(b, "\n") + "\n"
	off := func(p Pos) int {
		o := 0
		for i := 0; i < p.Line && i < len(b); i++ {
			o += len(b[i]) + 1
		}
		return o + p.Col
	}
	for i := len(hits) - 1; i >= 0; i-- {
		h := hits[i]
		text = text[:off(h.m.Start)] + h.text + text[off(h.m.End):]
	}
	return strings.TrimSuffix(text, "\n")
}

func checkSubs(t *testing.T, cases []sc) {
	t.Helper()
	for _, c := range cases {
		if got := c.run(t); got != c.want {
			t.Errorf(":s/%s/%s/ on %q = %q, want %q", c.pat, c.replacement(), c.in, got, c.want)
		}
	}
	if os.Getenv("ORIEL_ORACLE") != "" {
		oracle(t, cases)
	}
}

var errCode = regexp.MustCompile(`E\d+: `)

// oracleResults runs the cases in nvim.
func oracleResults(t *testing.T, cases []sc) []string {
	t.Helper()
	type job struct {
		Lines  []string `json:"lines"`
		Pre    []string `json:"pre"`
		Cmd    string   `json:"cmd"`
		Search string   `json:"search"`
	}
	var jobs []job
	for _, c := range cases {
		b := lines(c.in)
		j := job{Lines: b}
		if c.ic {
			j.Pre = append(j.Pre, "set ignorecase")
		}
		if c.scs {
			j.Pre = append(j.Pre, "set smartcase")
		}
		j.Pre = append(j.Pre, "silent! s\x01\\%$\\%^NOPE\x01"+c.last+"\x01e")
		if c.nomagic {
			j.Pre = append(j.Pre, "set nomagic")
		}
		offset := func(p Pos) int {
			o := 1
			for i := 0; i < p.Line; i++ {
				o += len(b[i]) + 1
			}
			return o + p.Col
		}
		if c.vis != nil {
			j.Pre = append(j.Pre, fmt.Sprintf("exe \"normal! %dgo%s%dgo\\<Esc>\"", offset(c.vis.Start), string(rune(c.vis.Mode)), offset(c.vis.End)))
		}
		for r, p := range c.marks {
			j.Pre = append(j.Pre, fmt.Sprintf("call setpos(\"'%c\", [0, %d, %d, 0])", r, p.Line+1, p.Col+1))
		}
		if c.cur != nil {
			j.Pre = append(j.Pre, fmt.Sprintf("call cursor(%d, %d)", c.cur.Line+1, c.cur.Col+1))
		}
		rng := "%"
		if c.line > 0 {
			rng = fmt.Sprint(c.line)
		}
		flags := "e"
		if c.g {
			flags += "g"
		}
		j.Cmd = rng + "s\x01" + c.pat + "\x01" + c.replacement() + "\x01" + flags
		if c.search {
			j.Cmd, j.Search = "", c.pat
		}
		jobs = append(jobs, j)
	}
	got := runNvim(t, jobs)
	for i := range got {
		if loc := errCode.FindStringIndex(got[i]); strings.HasPrefix(got[i], "error: ") && loc != nil {
			got[i] = "error: " + got[i][loc[0]:]
		}
	}
	return got
}

// oracle runs the cases in nvim and fails where nvim's result differs from the case's want.
func oracle(t *testing.T, cases []sc) {
	t.Helper()
	got := oracleResults(t, cases)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("oracle: :s/%s/%s/ on %q is %q in nvim, the case wants %q", c.pat, c.replacement(), c.in, got[i], c.want)
		}
	}
}

const oracleLua = `
local jobs = vim.json.decode(io.open(arg[1]):read('*a'))
local out = {}
for i, j in ipairs(jobs) do
  vim.cmd('silent! %bwipeout!')
  vim.cmd('set noignorecase nosmartcase magic')
  vim.api.nvim_buf_set_lines(0, 0, -1, false, j.lines)
  for _, c in ipairs(j.pre) do vim.cmd(c) end
  local ok, err = true, nil
  if j.cmd ~= '' then ok, err = pcall(vim.cmd, j.cmd) end
  if j.search ~= '' then
    vim.fn.cursor(1, 1)
    ok, err = pcall(vim.fn.searchpos, j.search, 'cnW')
    if ok then err = err[1] .. ':' .. err[2] end
  end
  local res
  if not ok then
    res = 'error: ' .. tostring(err)
  elseif j.search ~= '' then
    res = err
  else
    local ls = vim.api.nvim_buf_get_lines(0, 0, -1, false)
    for k, l in ipairs(ls) do ls[k] = l:gsub('\n', '\0') end
    res = table.concat(ls, '\n')
  end
  out[i] = res
end
io.open(arg[2], 'w'):write(vim.json.encode(out))
`

func runNvim(t *testing.T, jobs any) []string {
	t.Helper()
	nvim := os.Getenv("ORIEL_NVIM")
	if nvim == "" {
		nvim = "nvim"
	}
	dir := t.TempDir()
	in, out, script := filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json"), filepath.Join(dir, "oracle.lua")
	data, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte(oracleLua), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nvim, "--headless", "--clean", "-l", script, in, out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nvim: %v\n%s", err, b)
	}
	data, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	return got
}
