package gitui

import (
	"cmp"
	"context"
	"image"
	"os"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// diffContext is how many unchanged lines the diff shows around a change, as git diff does by default.
const diffContext = 3

// diffInput is what a file's diff is made from; the diff is made again when it changes.
type diffInput struct {
	rev        string // the base revision; "" for none
	path, old  string // repo paths: the file, and a rename's old name
	code       byte
	add, del   int
	size       int64
	mtime      int64
	buf        *text.Buffer // the open buffer it's read from, nil for the file on disk
	version    uint64
	repo, work string
}

// diffFile is one file's branch diff, made off the loop: its hunks with their word marks, and when styled, for the
// view, their highlights and function.
type diffFile struct {
	in      diffInput
	styled  bool
	binary  bool
	images  *imageDiff // a changed image's old and new versions, decoded
	newText []byte
	hunks   []diffHunk
	err     error
}

// diffHunk is a hunk as the view draws it.
type diffHunk struct {
	gitx.DiffHunk
	ids   []string
	fn    string       // the function it's in
	spans [][]kit.Span // each line's text, by highlight
	marks [][][2]int   // each line's changed words
}

// loadDiff makes a file's diff: the base revision's version against newText (the buffer's), or the file on disk
// when newText is nil. Highlights and each hunk's function, which only the view shows, are made when styled.
func loadDiff(r registry.Registry, in diffInput, newText []byte, styled bool) *diffFile {
	ctx := context.Background()
	repo := kit.RepoAt(r, in.repo)
	f := &diffFile{in: in, styled: styled}
	var old []byte
	if in.code != 'A' && in.code != '?' {
		old, _ = repo.FileAt(ctx, in.rev, cmp.Or(in.old, in.path))
	}
	if newText == nil && in.code != 'D' {
		b, err := os.ReadFile(in.work)
		if err != nil && !os.IsNotExist(err) {
			f.err = err
			return f
		}
		newText = b
	}
	f.newText = newText
	if kit.IsBinary(old) || kit.IsBinary(newText) {
		f.binary = true
		f.images = decodeImageDiff(in.path, old, newText, in.code)
		return f
	}
	hs := gitx.Unified(old, newText, diffContext)
	oldLines, newLines := gitx.SplitLines(old), gitx.SplitLines(newText)
	var oldHL, newHL [][]kit.Span
	var units []text.Unit
	if styled {
		oldHL, newHL = highlightLines(r, in.path, old, oldLines, hs, true), highlightLines(r, in.path, newText, newLines, hs, false)
		units = outline(r, in.path, newText)
	}
	for _, h := range hs {
		dh := diffHunk{DiffHunk: h, ids: h.IDs(), spans: make([][]kit.Span, len(h.Lines)), marks: make([][][2]int, len(h.Lines))}
		if styled {
			dh.fn = hunkFunc(h, units, newText, newLines, oldLines)
		}
		k := 0
		for _, c := range h.Changes {
			om, nm := c.Intraline()
			for k < len(h.Lines) && h.Lines[k].Op == ' ' {
				k++
			}
			for i := range om {
				dh.marks[k+i] = om[i]
			}
			k += len(om)
			for i := range nm {
				dh.marks[k+i] = nm[i]
			}
			k += len(nm)
		}
		for i, l := range h.Lines {
			text := strings.TrimRight(l.Text, "\r\n")
			switch {
			case l.Op == '-' && oldHL != nil:
				dh.spans[i] = oldHL[l.Old-1]
			case l.Op != '-' && newHL != nil:
				dh.spans[i] = newHL[l.New-1]
			default:
				dh.spans[i] = []kit.Span{{Text: text}}
			}
		}
		f.hunks = append(f.hunks, dh)
	}
	return f
}

// imageDiff is an image file's two versions: either is nil when the change adds or deletes the file.
type imageDiff struct {
	old, new         image.Image
	oldSize, newSize int
}

// decodeImageDiff decodes the versions of a changed image; nil when path isn't an image or neither decodes.
func decodeImageDiff(path string, old, new []byte, code byte) *imageDiff {
	if !kit.IsImage(path) {
		return nil
	}
	d := &imageDiff{oldSize: len(old), newSize: len(new)}
	if len(old) > 0 {
		d.old, _, _ = kit.DecodeImage(old)
	}
	if len(new) > 0 && code != 'D' {
		d.new, _, _ = kit.DecodeImage(new)
	}
	if d.old == nil && d.new == nil {
		return nil
	}
	return d
}

// highlightLines is each line of src that a hunk shows on its side, cut into runs by highlight; nil without a
// grammar.
func highlightLines(r registry.Registry, path string, src []byte, lines []string, hs []gitx.DiffHunk, old bool) [][]kit.Span {
	lang, ok := kit.LanguageFor(r, path, src)
	if !ok || len(src) == 0 {
		return nil
	}
	p, err := syntax.Borrow(lang)
	if err != nil {
		return nil
	}
	defer p.Release()
	p.Parse(src)
	starts := make([]int, len(lines)+1)
	for i, l := range lines {
		starts[i+1] = starts[i] + len(l)
	}
	out := make([][]kit.Span, len(lines))
	for _, h := range hs {
		start, n := h.NewStart, h.NewLines
		if old {
			start, n = h.OldStart, h.OldLines
		}
		from, to := max(start-1, 0), min(start-1+n, len(lines))
		if n == 0 || from >= to {
			continue
		}
		var hl []syntax.Span
		for start := starts[from]; start < starts[to]; start += 1 << 10 {
			end := min(start+1<<10, starts[to])
			spans, err := p.Highlights(start, end)
			if err != nil {
				break
			}
			for _, s := range spans {
				s.Range.Start, s.Range.End = max(s.Range.Start, start), min(s.Range.End, end)
				if s.Range.Start < s.Range.End {
					hl = append(hl, s)
				}
			}
		}
		for i := from; i < to; i++ {
			out[i] = kit.SplitSpans(strings.TrimRight(lines[i], "\r\n"), starts[i], hl)
		}
	}
	return out
}

// outline is the functions, methods and types of src, by tree-sitter.
func outline(r registry.Registry, path string, src []byte) []text.Unit {
	lang, ok := kit.LanguageFor(r, path, src)
	if !ok || len(src) == 0 {
		return nil
	}
	p, err := syntax.Borrow(lang)
	if err != nil {
		return nil
	}
	defer p.Release()
	p.Parse(src)
	us, _ := p.Outline()
	return us
}

// hunkFunc is what a hunk's header names after its @@: the smallest function, method or type around its first
// change, from its line up to its name ("func roundHalfEven"); without one, the line git would name.
func hunkFunc(h gitx.DiffHunk, units []text.Unit, src []byte, lines, oldLines []string) string {
	at, before := -1, 0
	for _, l := range h.Lines {
		if l.Op == ' ' {
			before = l.New
			continue
		}
		at = l.New - 1
		if l.New == 0 {
			at = max(before-1, 0) // a deletion: the line before it
		}
		break
	}
	if at >= 0 && at < len(lines) {
		off := 0
		for _, l := range lines[:at] {
			off += len(l)
		}
		best := -1
		for i, u := range units {
			if u.Range.Start <= off && off < u.Range.End && (best < 0 || u.Range.Len() < units[best].Range.Len()) {
				best = i
			}
		}
		if best >= 0 {
			u := units[best]
			first, _, _ := strings.Cut(string(src[u.Range.Start:u.Range.End]), "\n")
			if i := strings.Index(first, u.Name); i >= 0 {
				first = first[:i+len(u.Name)]
			}
			return strings.TrimSpace(first)
		}
	}
	return gitx.FuncLine(oldLines, h.OldStart)
}
