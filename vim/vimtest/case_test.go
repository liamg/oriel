package vimtest

import (
	"strings"
	"testing"
)

const sample = `# a comment
=== cw on a word acts like ce
ref:    change.txt *cw*
quote:  "Special case: When the cursor is in a word, \"cw\" and \"cW\" do not include the white space after a word"
given:  |foo bar
keys:   cwxy<Esc>
expect: x|y bar
mode:   normal
regs:   "" = "foo"   "1 = (unset)   "- = "foo"
marks:  a=1:0 'b=(unset)
jumps:  1:0 2:3
msg:    "E21: x"
win:    2/3
bufs:   1#  2%   4
undo:   |foo bar

=== multi-line
ref:    oracle nvim v0.11.7
follows: nvim
given:
  one
    |two

  four
keys:   j
expect:
  one
    two
  |
  four
`

func TestParse(t *testing.T) {
	cs, err := Parse("sample.vt", strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("got %d cases, want 2", len(cs))
	}
	c := cs[0]
	if c.Name != "cw on a word acts like ce" || c.Line != 2 || c.Ref != "change.txt *cw*" {
		t.Errorf("header: %q line %d ref %q", c.Name, c.Line, c.Ref)
	}
	if !strings.HasPrefix(c.Quote, `Special case: When the cursor is in a word, "cw"`) {
		t.Errorf("quote: %q", c.Quote)
	}
	if c.Given.String() != "|foo bar" || c.Expect.String() != "x|y bar" || c.Undo.String() != "|foo bar" {
		t.Errorf("buffers: %q %q %q", c.Given, c.Expect, c.Undo)
	}
	if c.RawKeys != "cwxy<Esc>" || len(c.Keys) != 5 || c.Mode != ModeNormal {
		t.Errorf("keys %q (%d), mode %q", c.RawKeys, len(c.Keys), c.Mode)
	}
	if regString(c.Regs['"']) != `"foo"` || c.Regs['1'] != nil || regString(c.Regs['-']) != `"foo"` || len(c.Regs) != 3 {
		t.Errorf("regs: %v", c.Regs)
	}
	if posString(c.Marks['a']) != "1:0" || c.Marks['b'] != nil || len(c.Marks) != 2 {
		t.Errorf("marks: %v", c.Marks)
	}
	if len(c.Jumps) != 2 || c.Jumps[1] != (Pos{1, 3}) {
		t.Errorf("jumps: %v", c.Jumps)
	}
	if c.Msg == nil || *c.Msg != "E21: x" {
		t.Errorf("msg: %v", c.Msg)
	}
	if c.Win == nil || *c.Win != (Win{2, 3}) {
		t.Errorf("win: %v", c.Win)
	}
	if c.Bufs == nil || *c.Bufs != "1# 2% 4" {
		t.Errorf("bufs: %v", c.Bufs)
	}
	m := cs[1]
	if got := strings.Join(m.Given.Lines, "/"); got != "one/  two//four" || m.Given.Cursor != (Pos{1, 2}) {
		t.Errorf("block given: %q %v", got, m.Given.Cursor)
	}
	if m.Expect.Cursor != (Pos{2, 0}) || m.Follows != "nvim" {
		t.Errorf("block expect: %v, follows %q", m.Expect.Cursor, m.Follows)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ref: x", "before the first ==="},
		{"=== a\ngiven: |a\nkeys: x", "required"},
		{"=== a\ngiven: |a\nexpect: |a", "keys is required"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nwat: 1", "unknown field"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nmode: nromal", "unknown mode"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nregs: a = 1", "want"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nmarks: a=0:1", "line:col"},
		{"=== a\ngiven: |a\ngiven: |a", "twice"},
		{"=== a\ngiven: a\nkeys: x\nexpect: |a", "no cursor"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nfollows: emacs", "follows"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nwin: 3/2", "current/count"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nwin: 1", "current/count"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nbufs: 2 1%", "ascending"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nbufs: 1%#", "ascending"},
		{"=== a\ngiven: |a\nkeys: x\nexpect: |a\nbufs: x", "ascending"},
	}
	for _, tt := range tests {
		_, err := Parse("t.vt", strings.NewReader(tt.in))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%q): got %v, want an error containing %q", tt.in, err, tt.want)
		}
	}
}

func TestParseTrailingEmptyLine(t *testing.T) {
	in := "=== e\nref: oracle nvim v0.11.7\ngiven:\n  |a\n  \n\nkeys: x\nexpect:\n  |\n\n"
	cs, err := Parse("t.vt", strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := cs[0].Given.Lines; len(got) != 2 || got[1] != "" {
		t.Errorf("given lines = %q, want [\"a\" \"\"]", got)
	}
	if got := cs[0].Expect.Lines; len(got) != 1 {
		t.Errorf("expect lines = %q, want one line", got)
	}
}

func TestCheckRef(t *testing.T) {
	tests := []struct {
		ref, quote, wantErr string
	}{
		{"motion.txt *w*", "[count] words forward.", ""},
		{"change.txt *cw*", "do not include the\n   white space after a word", ""},
		{"nvim/options.txt *'startofline'*", "", ""},
		{"oracle nvim v0.11.7", "", ""},
		{"", "", "no ref"},
		{"oracle", "", "names the nvim version"},
		{"motion.txt w", "", "want"},
		{"motion.txt *nosuchtag*", "", "no *nosuchtag*"},
		{"motion.txt *wo*", "", "no *wo*"},
		{"nosuch.txt *w*", "", "no such file"},
		{"../x.txt *w*", "", "bad spec path"},
		{"motion.txt *w*", "[count] wordz forward.", "is not in"},
		{"motion.txt *w*", "[count] WORDS forward.", "is not in the *w* section"},
		{"testdir/test_join.vim#Test_join_with_count", "", ""},
		{"testdir/test_join.vim#Test_join_with_count", "normal 10J", ""},
		{"testdir/test_join.vim#Test_join_with_count", "normal 11J", "is not in Test_join_with_count"},
		{"testdir/test_join.vim#Test_join_marks", "normal 10J", "is not in Test_join_marks"},
		{"testdir/test_join.vim#Test_no_such", "", "no function Test_no_such"},
		{"test_join.vim#Test_join_with_count", "", "testdir/"},
	}
	for _, tt := range tests {
		err := Case{Ref: tt.ref, Quote: tt.quote}.CheckRef()
		if tt.wantErr == "" && err != nil || tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
			t.Errorf("CheckRef(%q, %q) = %v, want %q", tt.ref, tt.quote, err, tt.wantErr)
		}
	}
}

func TestDiff(t *testing.T) {
	cs, err := Parse("t.vt", strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	c := cs[0]
	foo := "foo"
	good := Result{
		Buffer: c.Expect, Mode: ModeNormal,
		Regs:  map[rune]*string{'"': &foo, '1': nil, '-': &foo},
		Marks: map[rune]*Pos{'a': {0, 0}},
		Jumps: []Pos{{0, 0}, {1, 3}},
		Msg:   "E21: x",
		Win:   Win{2, 3},
		Bufs:  "1# 2% 4",
		Undo:  &c.Given,
	}
	if d := c.Diff(good); len(d) != 0 {
		t.Errorf("matching result: diffs %q", d)
	}
	bad := good
	bad.Buffer, _ = ParseBuffer("xy| bar")
	bad.Mode = ModeInsert
	bad.Regs = map[rune]*string{'"': &foo, '1': &foo, '-': &foo}
	bad.Marks = map[rune]*Pos{'a': {0, 0}, 'b': {0, 1}}
	bad.Jumps = nil
	bad.Msg = ""
	bad.Win = Win{1, 3}
	bad.Bufs = "1% 2# 4"
	bad.Undo = nil
	want := []string{"buffer", "mode", "register '1'", "mark 'b'", "jumps", "msg", "win", "bufs", "undo: not run"}
	d := c.Diff(bad)
	if len(d) != len(want) {
		t.Fatalf("got diffs %q, want %d", d, len(want))
	}
	for i := range want {
		if !strings.HasPrefix(d[i], want[i]) {
			t.Errorf("diff %d = %q, want %s", i, d[i], want[i])
		}
	}
}

func TestRangesAndTop(t *testing.T) {
	cs, err := Parse("x.vt", strings.NewReader("=== a\ngiven:\n  {1..3}\n  |x\n  {9..10}\nkeys: x\nexpect: |a\ntop: 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cs[0].Given.Lines, ","); got != "1,2,3,x,9,10" || cs[0].Given.Cursor != (Pos{3, 0}) {
		t.Errorf("given = %q at %v", got, cs[0].Given.Cursor)
	}
	if cs[0].Top == nil || *cs[0].Top != 4 {
		t.Errorf("top = %v", cs[0].Top)
	}
}
