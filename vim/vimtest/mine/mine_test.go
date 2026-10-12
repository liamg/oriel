package mine

import (
	"reflect"
	"strings"
	"testing"

	"github.com/liamg/oriel/vim/vimtest"
)

func TestConcatKeys(t *testing.T) {
	tests := []struct{ expr, want string }{
		{`"norm! Sfoobar\<esc>"`, "norm! Sfoobar<Esc>"},
		{`'norm! a''b'`, "norm! a'b"},
		{`"a\<C-R>-\tb\n"`, "a<C-R>-<Tab>b<NL>"},
		{`"x" .. 3 .. 'y' . "\e"`, "x3y<Esc>"},
		{`"\x41é\101"`, "AéA"},
		{`"\<lt>a"`, "<lt>a"},
		{`"<"`, "<lt>"},
	}
	for _, tt := range tests {
		keys, err := concatKeys(tt.expr)
		if err != nil {
			t.Errorf("concatKeys(%s): %v", tt.expr, err)
			continue
		}
		if got := vimtest.Notation(keys); got != tt.want {
			t.Errorf("concatKeys(%s) = %q, want %q", tt.expr, got, tt.want)
		}
	}
	for _, bad := range []string{`"a" .. x`, `"unterminated`, `"\<NoSuchKey>"`, `foo()`} {
		if _, err := concatKeys(bad); err == nil {
			t.Errorf("concatKeys(%s): want an error", bad)
		}
	}
}

func TestParseCall(t *testing.T) {
	name, args, ok := parseCall(`call assert_equal(['a, b', "c)"], getline(1, '$')) " comment`)
	if !ok || name != "assert_equal" || !reflect.DeepEqual(args, []string{`['a, b', "c)"]`, `getline(1, '$')`}) {
		t.Errorf("got %q %q %v", name, args, ok)
	}
	if _, _, ok := parseCall(`call foo() | call bar()`); ok {
		t.Error("a call followed by another command parsed as one call")
	}
}

func TestIsLiteral(t *testing.T) {
	vars := map[string]bool{"expected": true, "computed": false}
	for expr, want := range map[string]bool{
		`['a', "b\n", 3]`:  true,
		`'a' .. 'b'`:       true,
		`expected`:         true,
		`computed`:         false,
		`getline(1)`:       false,
		`repeat('a', 3)`:   false,
		`[0, 1, -1, 0]`:    true,
		`unknown_variable`: false,
	} {
		if got := isLiteral(expr, vars); got != want {
			t.Errorf("isLiteral(%s) = %v, want %v", expr, got, want)
		}
	}
}

func TestScan(t *testing.T) {
	tests := []struct {
		keys     string
		insert   bool
		category string
	}{
		{"dw", false, ""},
		{`3"a2yy`, false, ""},
		{"ciwfoo", true, ""},
		{"ciwfoo<Esc>x", false, ""},
		{"ofoo<CR>bar<Esc>", false, ""},
		{"d2aW", false, ""},
		{"g~iwgUU", false, ""},
		{"fxrz;", false, ""},
		{"i/<Esc>", false, ""},
		{"Rab<C-R>-<Esc>", false, ""},
		{"vwy", false, "visual/select mode"},
		{"dvj", false, "visual/select mode"},
		{"/foo<CR>", false, "search"},
		{"dn", false, "search"},
		{"qaxq", false, "macro"},
		{":s/a/b<CR>", false, "ex command"},
		{"zt", false, "folds/scrolling"},
		{"<C-D>", false, "screen-relative"},
		{"gqq", false, "formatting/filter/opfunc"},
		{"d", false, "incomplete command"},
		{`"=`, false, "special register"},
		{"ifoo<C-N>", false, "insert completion"},
		{"ia<C-O>x", false, "insert-mode key"},
	}
	for _, tt := range tests {
		insert, err := Scan(vimtest.ParseKeys(tt.keys))
		cat := ""
		if err != nil {
			cat = err.Category
		}
		if cat != tt.category || err == nil && insert != tt.insert {
			t.Errorf("Scan(%q) = %v, %v; want insert=%v category %q", tt.keys, insert, err, tt.insert, tt.category)
		}
	}
}

func TestKeysOf(t *testing.T) {
	tests := []struct {
		stmt   string
		isKeys bool
		keys   string
		reject string
	}{
		{"normal! dw", true, "dw", ""},
		{"norm 2J", true, "2J", ""},
		{"silent! normal! x", true, "x", ""},
		{`exe "norm! Sfoo\<Esc>"`, true, "Sfoo<Esc>", ""},
		{`exe ":norm! ciw'\<C-R>-'"`, true, "ciw'<C-R>-'", ""},
		{`exe "norm! " .. n .. "x"`, true, "", "keys not literal"},
		{`call feedkeys("i\<CR>x\<Esc>", 'xt')`, true, "i<CR>x<Esc>", ""},
		{`call feedkeys("ix", 't')`, true, "", "feedkeys without 'x'"},
		{`exe "set sw=2"`, false, "", ""},
		{`call setline(1, 'a')`, false, "", ""},
	}
	for _, tt := range tests {
		keys, isKeys, err := keysOf(tt.stmt)
		reject := ""
		if err != nil {
			reject = err.Category
		}
		if isKeys != tt.isKeys || reject != tt.reject || err == nil && vimtest.Notation(keys) != tt.keys {
			t.Errorf("keysOf(%s) = %q, %v, %v; want %q, %v, %q", tt.stmt, vimtest.Notation(keys), isKeys, err, tt.keys, tt.isKeys, tt.reject)
		}
	}
}

func TestFuncs(t *testing.T) {
	src := `" header
func Helper()
  return 1
endfunc

func Test_one()
  new
  call setline(1, ['a',
        \ 'b'])
  " a comment
  normal! dd | x
  call cursor(1, 1) | normal! x
  insert
text
.
  for i in range(2)
    if i
      normal! x
    endif
  endfor
endfunc
`
	fs := Funcs(src)
	if len(fs) != 1 || fs[0].Name != "Test_one" {
		t.Fatalf("Funcs = %+v", fs)
	}
	var got []string
	for _, s := range fs[0].Stmts {
		got = append(got, s.Text)
	}
	want := []string{
		"new",
		"call setline(1, ['a', 'b'])",
		"normal! dd | x",
		"call cursor(1, 1)",
		"normal! x",
		"insert\ntext\n.",
		"for i in range(2)\n    if i\n      normal! x\n    endif\n  endfor",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("statements:\n got %q\nwant %q", got, want)
	}
	if h := Helpers(src); !strings.Contains(h, "func Helper()") || strings.Contains(h, "Test_one") {
		t.Errorf("Helpers = %q", h)
	}
}

// fakeSim replays a scripted state: each Exec of keys applies the next state.
type fakeSim struct {
	states []vimtest.Buffer
	evals  map[string]any
	i      int
}

func (f *fakeSim) Exec(src string) error {
	if strings.Contains(src, "norm") && f.i < len(f.states)-1 {
		f.i++
	}
	return nil
}

func (f *fakeSim) Eval(expr string) (any, error) {
	if v, ok := f.evals[expr]; ok {
		return v, nil
	}
	if strings.Contains(expr, "nvim_get_option_info2") {
		return int64(1), nil
	}
	return nil, nil
}

func (f *fakeSim) State() (vimtest.Buffer, string, error) {
	return f.states[f.i], vimtest.ModeNormal, nil
}
func (f *fakeSim) Close() error { return nil }

func TestMineFunc(t *testing.T) {
	src := `func Test_x()
  new
  call setline(1, ['foo bar'])
  normal! dw
  call assert_equal('bar', getline(1))
  call assert_equal("foo ", @")
endfunc
`
	sim := &fakeSim{
		states: []vimtest.Buffer{{Lines: []string{"foo bar"}}, {Lines: []string{"bar"}}},
		evals:  map[string]any{"'bar'": "bar", "getline(1)": "bar", `"foo "`: "foo ", `@"`: "foo "},
	}
	oracle := func(c vimtest.Case) (vimtest.Result, error) {
		v := "foo "
		return vimtest.Result{Buffer: vimtest.Buffer{Lines: []string{"bar"}}, Regs: map[rune]*string{'"': &v}}, nil
	}
	res := File("test_x.vim", src, func() (Sim, error) { return sim, nil }, oracle)
	if res.Asserts != 2 || res.Converted != 2 || len(res.Cases) != 1 {
		t.Fatalf("result = %+v", res)
	}
	got := renderCase(res.Cases[0])
	want := strings.Join([]string{
		"=== Test_x #1",
		"ref:    testdir/test_x.vim#Test_x",
		"given:",
		"  |foo bar",
		"keys:   dw",
		"# cursor from oracle",
		"expect:",
		"  |bar",
		`regs:   "" = "foo "`,
		"",
	}, "\n")
	if got != want {
		t.Errorf("case:\n%s\nwant:\n%s", got, want)
	}
}
