package kit

import (
	"bytes"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// langSets are the registry's languages as a syntax.Set, made again when they change.
var langSets struct {
	sync.Mutex
	by map[registry.Registry]*langSet
}

type langSet struct {
	gen int
	set *syntax.Set
}

// WindowRegistry is the registry r stands for: a plugin's host is a view of its window's.
func WindowRegistry(r registry.Registry) registry.Registry {
	if h, ok := r.(interface{ WindowRegistry() registry.Registry }); ok {
		if w := h.WindowRegistry(); w != nil {
			return w
		}
	}
	return r
}

// Languages is the set of languages r has now, one set a window.
func Languages(r registry.Registry) *syntax.Set {
	ls, gen := r.Languages()
	r = WindowRegistry(r)
	langSets.Lock()
	defer langSets.Unlock()
	if langSets.by == nil {
		langSets.by = map[registry.Registry]*langSet{}
	}
	if s, ok := langSets.by[r]; ok && s.gen == gen {
		return s.set
	}
	var out []*syntax.Language
	for _, l := range ls {
		out = append(out, &syntax.Language{Name: l.Name, Extensions: l.Extensions, Filenames: l.Filenames, Shebangs: l.Shebangs,
			Grammar: l.Grammar, Queries: l.Queries})
	}
	s := &langSet{gen: gen, set: syntax.NewSet(out...)}
	langSets.by[r] = s
	return s.set
}

// ForgetLanguages drops r's set, once the window it belongs to has closed.
func ForgetLanguages(r registry.Registry) {
	langSets.Lock()
	defer langSets.Unlock()
	delete(langSets.by, WindowRegistry(r))
}

// LanguageFor is the language of the file at path, by its name or the first line of src.
func LanguageFor(r registry.Registry, path string, src []byte) (*syntax.Language, bool) {
	first, _, _ := bytes.Cut(src[:min(len(src), 512)], []byte("\n"))
	return Languages(r).ForFile(path, first)
}

var TLogFailLine = theme.DeclareOn("log.source.failing", "alpha(error, 8%)", "bg.base")

// HighlightedExcerpt is a few lines around a failing one, highlighted with tree-sitter.
type HighlightedExcerpt struct {
	path  string
	line  int
	lines []string
	First int // the line number of lines[0]
	Spans [][]Span
	Mark  bool            // the failing line, marked
	Title string          // the path its header shows; the file's own, home as ~, if ""
	Theme *theme.Resolved // drawn in this, not the window's: a theme's preview
	Size  float32         // the code's font size; 11.5 if 0
	Fill  bool            // take the height it's given, scrolling the code in it
}

type Span struct {
	Text    string
	Capture string
}

// HighlightAround is the lines from before lines above line to after lines below it, highlighted.
func HighlightAround(r registry.Registry, path string, src []byte, line, before, after int) *HighlightedExcerpt {
	lang, _ := LanguageFor(r, path, src)
	return highlightIn(lang, path, src, line, before, after)
}

// highlightIn is HighlightAround in lang; plain text when it's nil.
func highlightIn(lang *syntax.Language, path string, src []byte, line, before, after int) *HighlightedExcerpt {
	all := strings.Split(string(src), "\n")
	first := max(1, line-before)
	last := min(len(all), line+after)
	f := &HighlightedExcerpt{path: path, line: line, First: first}
	if first > last {
		return f
	}
	f.lines = all[first-1 : last]
	var hl []syntax.Span
	if lang != nil {
		if ps, err := syntax.Borrow(lang); err == nil {
			ps.Parse(src)
			start := OffsetOfLine(all, first)
			hl, _ = ps.Highlights(start, start+len(strings.Join(f.lines, "\n")))
			ps.Release()
		}
	}
	off := OffsetOfLine(all, first)
	for _, ln := range f.lines {
		f.Spans = append(f.Spans, SplitSpans(ln, off, hl))
		off += len(ln) + 1
	}
	return f
}

func OffsetOfLine(lines []string, n int) int {
	off := 0
	for _, l := range lines[:n-1] {
		off += len(l) + 1
	}
	return off
}

// SplitSpans cuts one line at offset off into runs by highlight capture.
func SplitSpans(ln string, off int, hl []syntax.Span) []Span {
	var out []Span
	pos := 0
	for _, h := range hl {
		s, e := max(h.Range.Start-off, pos), min(h.Range.End-off, len(ln))
		if e <= s {
			continue
		}
		if s > pos {
			out = append(out, Span{Text: ln[pos:s]})
		}
		out = append(out, Span{Text: ln[s:e], Capture: h.Capture})
		pos = e
	}
	if pos < len(ln) {
		out = append(out, Span{Text: ln[pos:]})
	}
	return out
}

func (f *HighlightedExcerpt) Build(c *ui.Ctx, n *core.Notification) {
	th := c.Theme()
	if f.Theme != nil {
		th = f.Theme
	}
	home, _ := os.UserHomeDir()
	shown := f.path
	if home != "" && strings.HasPrefix(shown, home+string(filepath.Separator)) {
		shown = "~" + shown[len(home):]
	}
	if f.Title != "" {
		shown = f.Title
	}
	size, lineH, numW := float32(11.5), float32(18), float32(22)
	if f.Size != 0 {
		size, lineH, numW = f.Size, f.Size*1.6, f.Size*2
	}
	box, lines := ui.Style{W: ui.Flex(1), Fill: th.C(theme.BgBase), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true}, ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 6}}
	if f.Fill {
		box.H, lines.H, lines.ScrollY = ui.Flex(1), ui.Flex(1), true
	}
	c.Column("src", box, func(c *ui.Ctx) {
		c.Row("path", ui.Style{W: ui.Flex(1), H: ui.Px(24), Pad: ui.PadXY(10, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text(nil, shown, Mono(10.5, typeset.Normal, th.C(theme.FgFaint)))
		})
		c.Column("lines", lines, func(c *ui.Ctx) {
			for i, ln := range f.Spans {
				no := f.First + i
				failing := f.Mark && no == f.line
				fill := color.NRGBA{}
				if failing {
					fill = th.C(TLogFailLine)
				}
				c.Row(no, ui.Style{W: ui.Flex(1), H: ui.Px(lineH), Gap: 10, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
					edge := color.NRGBA{}
					if failing {
						edge = th.C(theme.Error)
					}
					c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
					c.Box("no", ui.Style{W: ui.Px(numW)}, func(c *ui.Ctx) {
						c.Text(nil, fmt.Sprint(no), Mono(size-0.5, typeset.Normal, th.C(theme.FgGhost)))
					})
					c.Row("code", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
						for j, s := range ln {
							col := th.C(theme.Fg)
							if s.Capture != "" {
								col = th.Syntax("@" + s.Capture).FG
							}
							c.Text(j, ExpandTabs(s.Text), Mono(size, typeset.Normal, col))
						}
					})
				})
			}
		})
	})
}

// FileLines are src's lines, without their endings.
func FileLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}
