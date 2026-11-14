package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HunkKind is what a hunk does to the lines it covers, for the gutter.
type HunkKind int

const (
	Added HunkKind = iota
	Modified
	Deleted
)

func (k HunkKind) String() string { return [...]string{"added", "modified", "deleted"}[k] }

// Hunk is a run of changed lines between two versions of a file, numbered as git diff -U0 numbers them: 1-based,
// and a side with no lines starts at the line before the change.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Old, New           []string // the lines, with their line endings
}

func (h Hunk) Kind() HunkKind {
	switch {
	case h.OldLines == 0:
		return Added
	case h.NewLines == 0:
		return Deleted
	}
	return Modified
}

// Header is the hunk's @@ line, as git writes it with no context.
func (h Hunk) Header() string {
	side := func(start, n int) string {
		if n == 1 {
			return fmt.Sprint(start)
		}
		return fmt.Sprintf("%d,%d", start, n)
	}
	return fmt.Sprintf("@@ -%s +%s @@", side(h.OldStart, h.OldLines), side(h.NewStart, h.NewLines))
}

// Equal reports whether two hunks change the same lines in the same way.
func (h Hunk) Equal(o Hunk) bool {
	return h.OldStart == o.OldStart && h.OldLines == o.OldLines && h.NewStart == o.NewStart && h.NewLines == o.NewLines &&
		strings.Join(h.Old, "") == strings.Join(o.Old, "") && strings.Join(h.New, "") == strings.Join(o.New, "")
}

// SplitLines splits text into lines, each with its line ending; a last line without one is kept as it is.
func SplitLines(b []byte) []string {
	var out []string
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, string(b))
			break
		}
		out = append(out, string(b[:i+1]))
		b = b[i+1:]
	}
	return out
}

// DiffLines is the hunks that turn old into new: Myers' shortest edit script, with no context.
func DiffLines(old, new []byte) []Hunk {
	return diffLines(SplitLines(old), SplitLines(new))
}

func diffLines(a, b []string) []Hunk {
	// Strip the common ends, then run Myers' O(ND) algorithm on what's left.
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ops := myers(a[pre:len(a)-suf], b[pre:len(b)-suf])
	var hs []Hunk
	i, j := pre, pre
	var cur *Hunk
	flush := func() {
		if cur != nil {
			if cur.OldLines == 0 {
				cur.OldStart--
			}
			if cur.NewLines == 0 {
				cur.NewStart--
			}
			hs = append(hs, *cur)
			cur = nil
		}
	}
	for _, op := range ops {
		switch op {
		case '=':
			flush()
			i++
			j++
		case '-':
			if cur == nil {
				cur = &Hunk{OldStart: i + 1, NewStart: j + 1}
			}
			cur.Old = append(cur.Old, a[i])
			cur.OldLines++
			i++
		case '+':
			if cur == nil {
				cur = &Hunk{OldStart: i + 1, NewStart: j + 1}
			}
			cur.New = append(cur.New, b[j])
			cur.NewLines++
			j++
		}
	}
	flush()
	return hs
}

// myersMaxD is the most edits myers looks for a shortest script in; past it, a and b are taken as replaced whole.
// Its trace holds about D² ints, so this keeps a rewrite's diff to tens of megabytes.
const myersMaxD = 2000

// myers is the edit script from a to b: '=' keep, '-' delete from a, '+' insert from b; deletions before insertions
// within a change, as git writes them.
func myers(a, b []string) []byte {
	n, m := len(a), len(b)
	if n == 0 || m == 0 || n+m == 0 {
		return append(bytes.Repeat([]byte{'-'}, n), bytes.Repeat([]byte{'+'}, m)...)
	}
	most := n + m
	off := most
	v := make([]int, 2*most+2)
	var trace []diagonals
	for d := 0; d <= most; d++ {
		if d > myersMaxD {
			return append(bytes.Repeat([]byte{'-'}, n), bytes.Repeat([]byte{'+'}, m)...)
		}
		lo, hi := max(off-d-1, 0), min(off+d+2, len(v))
		trace = append(trace, diagonals{lo: lo, v: append([]int(nil), v[lo:hi]...)})
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				return backtrack(trace, d, n, m, off)
			}
		}
	}
	return nil
}

// diagonals are v as a step of myers found it, over the diagonals that step reads (its d±1 either side), from lo.
type diagonals struct {
	lo int
	v  []int
}

func (g diagonals) at(i int) int { return g.v[i-g.lo] }

func backtrack(trace []diagonals, d, n, m, off int) []byte {
	var ops []byte
	x, y := n, m
	for ; d > 0; d-- {
		v := trace[d]
		k := x - y
		var pk int
		if k == -d || k != d && v.at(off+k-1) < v.at(off+k+1) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := v.at(off + pk)
		py := px - pk
		for x > px && y > py {
			ops = append(ops, '=')
			x--
			y--
		}
		if x == px {
			ops = append(ops, '+')
			y--
		} else {
			ops = append(ops, '-')
			x--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, '=')
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	// Within each run of changes, deletions first.
	for i := 0; i < len(ops); {
		if ops[i] == '=' {
			i++
			continue
		}
		j := i
		dels := 0
		for j < len(ops) && ops[j] != '=' {
			if ops[j] == '-' {
				dels++
			}
			j++
		}
		for k := i; k < j; k++ {
			if k-i < dels {
				ops[k] = '-'
			} else {
				ops[k] = '+'
			}
		}
		i = j
	}
	return ops
}

// applyHunk is base with h, a hunk of the diff from base, applied: its old lines replaced by its new ones.
func applyHunk(base []string, h Hunk) []string {
	at := h.OldStart - 1
	if h.OldLines == 0 {
		at = h.OldStart
	}
	out := append([]string(nil), base[:at]...)
	out = append(out, h.New...)
	return append(out, base[at+h.OldLines:]...)
}

// revertHunk is target with h, a hunk of the diff to target, taken back out: its new lines replaced by its old ones.
func revertHunk(target []string, h Hunk) []string {
	return applyHunk(target, Hunk{OldStart: h.NewStart, OldLines: h.NewLines, New: h.Old})
}

// blob is a file's contents at a revision spec (":path" for the index, "HEAD:path"), and whether it's there.
func (r Repo) blob(ctx context.Context, spec string) ([]byte, bool) {
	out, err := r.git(ctx, "cat-file", "blob", spec)
	if err != nil {
		return nil, false
	}
	return out, true
}

// IndexFile is a file as staged, or HEAD's if it has no staged changes; ok is false when it isn't in the index.
func (r Repo) IndexFile(ctx context.Context, path string) ([]byte, bool) {
	return r.blob(ctx, ":"+path)
}

// HeadFile is a file as HEAD has it.
func (r Repo) HeadFile(ctx context.Context, path string) ([]byte, bool) {
	return r.blob(ctx, "HEAD:"+path)
}

// FileAt is a file as a revision has it ("HEAD", a merge-base's hash), and whether it's there.
func (r Repo) FileAt(ctx context.Context, rev, path string) ([]byte, bool) {
	if rev == "" {
		return nil, false
	}
	if isHash(rev) {
		if b, ok, err := r.object(ctx, rev+":"+path); err == nil {
			return b, ok
		}
	}
	return r.blob(ctx, rev+":"+path)
}

// isHash says rev is a commit's hash, whole or abbreviated, which names the same tree for as long as a batch runs.
func isHash(rev string) bool {
	return len(rev) >= 7 && len(rev) <= 64 && strings.Trim(rev, "0123456789abcdef") == ""
}

// blobRead is blob, telling a file that isn't there (ok false, nil error) from git failing to say.
func (r Repo) blobRead(ctx context.Context, spec string) ([]byte, bool, error) {
	out, err := r.git(ctx, "cat-file", "blob", spec)
	if err == nil {
		return out, true, nil
	}
	_, verr := r.git(ctx, "rev-parse", "--verify", "--quiet", spec)
	if exit := (*exec.ExitError)(nil); errors.As(verr, &exit) && exit.ExitCode() == 1 {
		return nil, false, nil // no such file there
	}
	return nil, false, err
}

// UnstagedHunks are the hunks from the index's version of path to contents: a buffer's text, saved or not. A file
// not in the index is all added.
func (r Repo) UnstagedHunks(ctx context.Context, path string, contents []byte) ([]Hunk, error) {
	idx, _, err := r.blobRead(ctx, ":"+path)
	if err != nil {
		return nil, err
	}
	return DiffLines(idx, contents), nil
}

// StagedHunks are the hunks from HEAD's version of path to the index's; none when the index hasn't it.
func (r Repo) StagedHunks(ctx context.Context, path string) ([]Hunk, error) {
	head, _, err := r.blobRead(ctx, "HEAD:"+path)
	if err != nil {
		return nil, err
	}
	idx, ok, err := r.blobRead(ctx, ":"+path)
	if err != nil || !ok {
		return nil, err
	}
	return DiffLines(head, idx), nil
}

// StageHunk stages one hunk of the change from the index to contents, leaving the work tree alone.
func (r Repo) StageHunk(ctx context.Context, path string, contents []byte, h Hunk) error {
	idx, _ := r.IndexFile(ctx, path)
	if !hasHunk(DiffLines(idx, contents), h) {
		return fmt.Errorf("%s: the hunk %s isn't unstaged any more", path, h.Header())
	}
	return r.writeIndex(ctx, path, strings.Join(applyHunk(SplitLines(idx), h), ""), true)
}

// UnstageHunk takes one staged hunk of path back out of the index.
func (r Repo) UnstageHunk(ctx context.Context, path string, h Hunk) error {
	head, inHead := r.HeadFile(ctx, path)
	idx, _ := r.IndexFile(ctx, path)
	if !hasHunk(DiffLines(head, idx), h) {
		return fmt.Errorf("%s: the hunk %s isn't staged any more", path, h.Header())
	}
	return r.writeIndex(ctx, path, strings.Join(revertHunk(SplitLines(idx), h), ""), inHead)
}

func hasHunk(hs []Hunk, h Hunk) bool {
	for _, x := range hs {
		if x.Equal(h) {
			return true
		}
	}
	return false
}

// writeIndex stages contents as path, with the mode it has in the index, or the work tree's. An empty file that
// keep is false for leaves the index instead: an added file with all of it unstaged.
func (r Repo) writeIndex(ctx context.Context, path, contents string, keep bool) error {
	if contents == "" && !keep {
		_, err := r.git(ctx, "rm", "-q", "--cached", "--", path)
		return err
	}
	mode := "100644"
	if out, err := r.git(ctx, "ls-files", "-s", "--", path); err == nil && len(out) > 6 {
		mode = string(out[:6])
	} else if st, err := os.Stat(filepath.Join(r.Dir, path)); err == nil && st.Mode()&0o111 != 0 {
		mode = "100755"
	}
	cmd := command(ctx, "-C", r.Dir, "hash-object", "-w", "--stdin", "--path="+path)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.Stdin = strings.NewReader(contents)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git hash-object: %w", err)
	}
	_, err = r.git(ctx, "update-index", "--add", "--cacheinfo", mode+","+strings.TrimSpace(string(out))+","+path)
	return err
}
