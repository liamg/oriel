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
		from     State
		insert   bool
		category string
	}{
		{"dw", State{}, false, ""},
		{`3"a2yy`, State{}, false, ""},
		{"ciwfoo", State{}, true, ""},
		{"ciwfoo<Esc>x", State{}, false, ""},
		{"ofoo<CR>bar<Esc>", State{}, false, ""},
		{"d2aW", State{}, false, ""},
		{"g~iwgUU", State{}, false, ""},
		{"fxrz;", State{}, false, ""},
		{"i/<Esc>", State{}, false, ""},
		{"Rab<C-R>-<Esc>", State{}, false, ""},
		{"vwy", State{}, false, ""},
		{"Vjd", State{}, false, ""},
		{"<C-V>2j$Afoo<Esc>", State{}, false, ""},
		{"<C-V>jIx", State{}, true, ""},
		{"viwoawU", State{}, false, ""},
		{`v3l"ap`, State{}, false, ""},
		{"vjrx", State{}, false, ""},
		{"vjcx", State{}, true, ""},
		{"gvd", State{}, false, ""},
		{"vVd", State{}, false, ""},
		{"d", State{Visual: "v"}, false, ""},
		{"j", State{Visual: "V"}, false, ""},
		{"/foo<CR>", State{}, false, ""},
		{"?a\\/b?e<CR>x", State{}, false, ""},
		{"d/foo/e<CR>", State{}, false, ""},
		{"dnN*#g*", State{}, false, ""},
		{"/fo<BS><BS><BS><BS>x", State{}, false, ""},
		{"/<C-R>a<CR>", State{}, false, ""},
		{"/foo", State{}, false, "incomplete command"},
		{"/foo<Up><CR>", State{}, false, "command-line key"},
		{"/foo<Esc>", State{}, false, "command-line key"},
		{":s/a/b<CR>", State{}, false, ""},
		{":%s/x\\|y/z/g<CR>", State{}, false, ""},
		{":'<,'>s/a/b/gc<CR>yyq", State{}, false, ""},
		{"vj:s/a/b/<CR>", State{}, false, ""},
		{":2,$-1d a<CR>:.t0<CR>:1,2m$<CR>:j!<CR>:3><CR>", State{}, false, ""},
		{":g/foo/normal Ax<CR>", State{}, false, ""},
		{":v/./d<CR>:g!/x/s//y/<CR>", State{}, false, ""},
		{":set sw=2 et noic<CR>", State{}, false, ""},
		{":&&<CR>&g&:~<CR>", State{}, false, ""},
		{":5<CR>", State{}, false, ""},
		{":sort<CR>", State{}, false, "ex command"},
		{":set tw=10<CR>", State{}, false, "ex command"},
		{":s/a/b/|s/c/d/<CR>", State{}, false, "ex command"},
		{":g/a/g/b/d<CR>", State{}, false, "ex command"},
		{":g/a/normal <C-V><CR>", State{}, false, "command-line key"},
		{":<C-R>a<CR>", State{}, false, "ex command"},
		{"d:foo<CR>", State{}, false, "ex command"},
		{"@:", State{}, false, "ex command"},
		{"qaxjq@a2@@", State{}, false, ""},
		{"x", State{Recording: true}, false, ""},
		{"q:", State{}, false, "windows"},
		{"vgh", State{}, false, "select mode"},
		{"gh", State{}, false, "select mode"},
		{"dgn", State{}, false, "gn/gN"},
		{"vjzf", State{}, false, "folds/scrolling"},
		{"vj=", State{}, false, "formatting/filter/opfunc"},
		{"dvj", State{}, false, "forced motion"},
		{"zt", State{}, false, "folds/scrolling"},
		{"<C-D>", State{}, false, "screen-relative"},
		{"gqq", State{}, false, "formatting/filter/opfunc"},
		{"d", State{}, false, "incomplete command"},
		{`"=`, State{}, false, "special register"},
		{"ifoo<C-N>", State{}, false, "insert completion"},
		{"ia<C-O>x", State{}, false, "insert-mode key"},
	}
	for _, tt := range tests {
		_, insert, err := Scan(vimtest.ParseKeys(tt.keys), tt.from)
		cat := ""
		if err != nil {
			cat = err.Category
		}
		if cat != tt.category || err == nil && insert != tt.insert {
			t.Errorf("Scan(%q) = %v, %v; want insert=%v category %q", tt.keys, insert, err, tt.insert, tt.category)
		}
	}
}

func TestScanState(t *testing.T) {
	tests := []struct {
		keys     string
		from, to State
	}{
		{"vj", State{}, State{Visual: "v"}},
		{"<C-V>jV", State{}, State{Visual: "V"}},
		{"Vjd", State{}, State{}},
		{"y", State{Visual: "v"}, State{}},
		{"gv", State{}, State{Visual: "?"}},
		{"vj:s/a/b<CR>", State{}, State{}},
		{"qax", State{}, State{Recording: true}},
		{"jq", State{Recording: true}, State{}},
	}
	for _, tt := range tests {
		to, _, err := Scan(vimtest.ParseKeys(tt.keys), tt.from)
		if err != nil || to != tt.to {
			t.Errorf("Scan(%q, %+v) = %+v, %v; want %+v", tt.keys, tt.from, to, err, tt.to)
		}
	}
}

func TestCheckEx(t *testing.T) {
	tests := []struct {
		line    string
		confirm bool
		ok      bool
	}{
		{"%s/a/b/g", false, true},
		{"s#a#b#gc", true, true},
		{"'<,'>s/a\\/b/c/", false, true},
		{"/foo/,/bar/-1d", false, true},
		{"silent! %s/x//", false, true},
		{"1,3co$", false, true},
		{"2pu a", false, true},
		{"g/a/s/b/c/c", true, true},
		{"set ws noscs report=0", false, true},
		{"norm! dw", false, true},
		{"put =range(3)", false, false},
		{"set ws=1", false, false},
		{"set sw", false, false},
		{"sno/a/b/", false, false},
		{"p", false, false},
		{"new", false, false},
		{"call foo()", false, false},
		{"norm vgh", false, false},
	}
	for _, tt := range tests {
		confirm, err := checkEx(tt.line, false)
		if (err == nil) != tt.ok || confirm != tt.confirm {
			t.Errorf("checkEx(%q) = %v, %v; want confirm=%v ok=%v", tt.line, confirm, err, tt.confirm, tt.ok)
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
		{`exe "normal /x\<CR>"`, true, "/x<CR>", ""},
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
