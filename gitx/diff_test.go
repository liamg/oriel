package gitx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// patchOf is a unified diff's hunks as git writes them: each header, then its lines.
func patchOf(hs []DiffHunk) string {
	var b strings.Builder
	for _, h := range hs {
		b.WriteString(h.Header() + "\n")
		for _, l := range h.Lines {
			b.WriteString(string(l.Op) + strings.TrimRight(l.Text, "\n") + "\n")
		}
	}
	return b.String()
}

// gitPatch is git diff -U<n>'s hunks for path, without its file header or the text after each @@.
func gitPatch(t *testing.T, r *gittest.Repo, n string, path string) string {
	out := r.Git("diff", "-U"+n, "--no-color", "--", path)
	var b strings.Builder
	in := false
	for _, l := range strings.SplitAfter(out, "\n") {
		if strings.HasPrefix(l, "@@") {
			in = true
			end := strings.Index(l[2:], "@@") + 4
			b.WriteString(l[:end] + "\n")
			continue
		}
		if in && l != "" && !strings.HasPrefix(l, `\`) {
			b.WriteString(l)
		}
	}
	return b.String()
}

// The diff view's hunks are git's, with git's context and line numbers: changes within twice the context share
// a hunk.
// ref: oracle git
func TestUnifiedMatchesGit(t *testing.T) {
	r, work := hunkFixture(t)
	for _, n := range []string{"3", "1", "6"} {
		ctx := map[string]int{"3": 3, "1": 1, "6": 6}[n]
		got := patchOf(Unified([]byte(gittest.Lines(40)), []byte(work), ctx))
		if want := gitPatch(t, r, n, "f.txt"); got != want {
			t.Errorf("-U%s:\n%s\nwant (git diff):\n%s", n, got, want)
		}
	}
	// A new file and a file emptied.
	r.Write(map[string]string{"new.txt": "a\nb\n"})
	r.Git("add", "-N", "new.txt")
	if got, want := patchOf(Unified(nil, []byte("a\nb\n"), 3)), gitPatch(t, r, "3", "new.txt"); got != want {
		t.Errorf("a new file:\n%s\nwant:\n%s", got, want)
	}
	r.Write(map[string]string{"f.txt": ""})
	if got, want := patchOf(Unified([]byte(gittest.Lines(40)), nil, 3)), gitPatch(t, r, "3", "f.txt"); got != want {
		t.Errorf("an emptied file:\n%s\nwant:\n%s", got, want)
	}
}

// Each line of a hunk has its numbers on the sides it's on.
func TestUnifiedNumbersLines(t *testing.T) {
	hs := Unified([]byte("a\nb\nc\n"), []byte("a\nB\nc\nd\n"), 3)
	if len(hs) != 1 {
		t.Fatalf("%d hunks", len(hs))
	}
	var got []string
	for _, l := range hs[0].Lines {
		got = append(got, string(l.Op)+strings.TrimSpace(l.Text)+":"+strconv.Itoa(l.Old)+","+strconv.Itoa(l.New))
	}
	if s := strings.Join(got, " "); s != " a:1,1 -b:2,0 +B:0,2  c:3,3 +d:0,4" {
		t.Errorf("lines %q", s)
	}
	if len(hs[0].Changes) != 2 || len(hs[0].IDs()) != 2 {
		t.Errorf("changes %v", hs[0].Changes)
	}
}

// A change's ID is its content: it stays as other changes in the file come and go, and differs from another change.
func TestHunkIDFollowsContent(t *testing.T) {
	base := gittest.Lines(40)
	one := strings.Replace(base, "line 3\n", "three\n", 1)
	two := strings.Replace(one, "line 30\n", "thirty\n", 1)
	a, b := Unified([]byte(base), []byte(one), 3), Unified([]byte(base), []byte(two), 3)
	if len(a) != 1 || len(b) != 2 {
		t.Fatalf("%d and %d hunks", len(a), len(b))
	}
	if a[0].IDs()[0] != b[0].IDs()[0] {
		t.Error("a change's ID moved when another hunk was added")
	}
	if b[0].IDs()[0] == b[1].IDs()[0] {
		t.Error("two changes share an ID")
	}
}

// gitWordDiff is the words git's --word-diff takes out of a and puts into b, with words as Intraline splits them:
// letters, digits and _, or any other single character.
func gitWordDiff(t *testing.T, a, b string) (del, add []string) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte(a+"\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b"), []byte(b+"\n"), 0o644)
	cmd := exec.Command("git", "diff", "--no-index", "--word-diff=porcelain", "--word-diff-regex=[[:alnum:]_]+|[^[:space:]]", "a", "b")
	cmd.Dir = dir
	out, _ := cmd.Output()
	_, body, _ := strings.Cut(string(out), "\n@@")
	for _, l := range strings.Split(body, "\n")[1:] {
		if l == "" {
			continue
		}
		switch l[0] {
		case '-':
			del = append(del, strings.Fields(l[1:])...)
		case '+':
			add = append(add, strings.Fields(l[1:])...)
		}
	}
	return del, add
}

func cut(s string, rs [][2]int) []string {
	var out []string
	for _, r := range rs {
		for _, w := range words(s[r[0]:r[1]]) {
			if strings.TrimSpace(w) != "" {
				out = append(out, w)
			}
		}
	}
	return out
}

// The words Intraline marks are the words git's word diff takes out and puts in.
// ref: oracle git
func TestIntralineMatchesGitWordDiff(t *testing.T) {
	for _, c := range [][2]string{
		{"\tr := scaled.Round(0)", "\tt := scaled.Truncate(0)"},
		{"\treturn r.Shift(-p)", "\treturn t.Shift(-p)"},
		{"\tp := precision[amt.Currency]", "\tp, ok := currency.Precision(amt.Currency)"},
		{"func Round(v int) int {", "func Round(v int, p int32) int {"},
	} {
		del, add := Intraline(c[0], c[1])
		wd, wa := gitWordDiff(t, c[0], c[1])
		gotDel, gotAdd := strings.Join(cut(c[0], del), " "), strings.Join(cut(c[1], add), " ")
		var wantDel, wantAdd []string
		for _, w := range wd {
			wantDel = append(wantDel, words(w)...)
		}
		for _, w := range wa {
			wantAdd = append(wantAdd, words(w)...)
		}
		if gotDel != strings.Join(wantDel, " ") || gotAdd != strings.Join(wantAdd, " ") {
			t.Errorf("%q → %q: marks -%q +%q, git's word diff -%q +%q", c[0], c[1], gotDel, gotAdd, wantDel, wantAdd)
		}
	}
}

// Lines with too little in common get no word marks: the whole line is the change.
func TestIntralineSkipsUnlikeLines(t *testing.T) {
	if del, add := Intraline("\tdiff := scaled.Sub(t).Abs()", "\tif ok { return nil }"); del != nil || add != nil {
		t.Errorf("unlike lines marked: %v %v", del, add)
	}
	old, new := Hunk{Old: []string{"a := 1\n", "gone\n"}, New: []string{"a := 2\n"}}.Intraline()
	if len(old) != 2 || len(new) != 1 || len(old[0]) != 1 || old[1] != nil {
		t.Errorf("pairs %v %v", old, new)
	}
}

// With no grammar, a hunk's header names the line git's default funcname would: the last before it that starts
// with a letter, _ or $.
// ref: oracle git
func TestFuncLineMatchesGit(t *testing.T) {
	src := "package p\n\nfunc a() {\n\tx := 1\n\ty := 2\n\tz := 3\n\tw := 4\n}\n\nfunc b() {\n\tq := 1\n}\n"
	r := gittest.New(t)
	r.Write(map[string]string{"p.txt": src})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"p.txt": strings.Replace(strings.Replace(src, "w := 4", "w := 5", 1), "q := 1", "q := 2", 1)})
	out := r.Git("diff", "-U0", "--no-color", "--", "p.txt")
	lines := SplitLines([]byte(src))
	var got, want []string
	for _, h := range Unified([]byte(src), []byte(strings.Replace(strings.Replace(src, "w := 4", "w := 5", 1), "q := 1", "q := 2", 1)), 0) {
		got = append(got, h.Header()+" "+FuncLine(lines, h.OldStart))
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "@@") {
			want = append(want, l)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("headers:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
