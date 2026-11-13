package kit

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ThreadMarkdown is how a thread's comments draw their markdown: images found from its project's folder (the primary
// project's, or the first, when it isn't anchored), all of them one lightbox group.
func ThreadMarkdown(se core.SessionState, t core.Thread) MarkdownOpts {
	o := CommentMarkdown()
	o.Group, o.Help = "thread:"+string(t.ID), "agent/requests#request"
	for i, p := range se.Projects {
		if t.Anchor != nil && p.Name == t.Anchor.Project || t.Anchor == nil && p.Primary || o.Dir == "" && i == 0 {
			o.Dir = p.Dir
		}
	}
	return o
}

// MDSpans is a paragraph's inline markdown as spans: `code`, *emphasis*, **strong**, ~~struck~~, and [links](url) as
// their text. Emphasis follows CommonMark's delimiter runs (spec/gfm, "Emphasis and strong emphasis"): a run opens or
// closes by what flanks it, so a snake_case name or 2 * 3 * 4 stays as written.
func MDSpans(s string) []widget.Span {
	toks := mdTokens(s)
	mdEmphasis(toks)
	var out []widget.Span
	var it, bold, strike int
	for _, t := range toks {
		switch {
		case t.code:
			out = append(out, widget.Span{Text: t.text, Code: true, Italic: it > 0, Bold: bold > 0, Strike: strike > 0})
			continue
		case t.delim != 0:
			// What a run didn't use is text; what it used opens or closes, after its leftovers when it closes and
			// before them when it opens.
			lit := strings.Repeat(string(t.delim), t.n)
			if t.closes != (mdStyle{}) {
				it, bold, strike = it-t.closes.it, bold-t.closes.bold, strike-t.closes.strike
			}
			if lit != "" {
				out = mdAppend(out, widget.Span{Text: lit, Italic: it > 0, Bold: bold > 0, Strike: strike > 0})
			}
			it, bold, strike = it+t.opens.it, bold+t.opens.bold, strike+t.opens.strike
			continue
		}
		out = mdAppend(out, widget.Span{Text: t.text, Italic: it > 0, Bold: bold > 0, Strike: strike > 0})
	}
	return out
}

// mdAppend adds sp to out, joined to the last span when they're set alike.
func mdAppend(out []widget.Span, sp widget.Span) []widget.Span {
	if n := len(out); n > 0 && !out[n-1].Code && !sp.Code && out[n-1].Italic == sp.Italic && out[n-1].Bold == sp.Bold && out[n-1].Strike == sp.Strike {
		out[n-1].Text += sp.Text
		return out
	}
	return append(out, sp)
}

// mdStyle counts emphasis a delimiter run opens or closes.
type mdStyle struct{ it, bold, strike int }

// mdTok is a piece of a paragraph: text, a code span, or a delimiter run of *, _ or ~.
type mdTok struct {
	text          string
	code          bool
	delim         byte
	n, orig       int // the run's characters left unused, and its length as written
	open, close   bool
	opens, closes mdStyle
}

func mdTokens(s string) []mdTok {
	var toks []mdTok
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, mdTok{text: b.String()})
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '`':
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				flush()
				toks = append(toks, mdTok{text: s[i+1 : i+1+j], code: true})
				i += j + 1
				continue
			}
		case c == '[':
			if j := strings.Index(s[i:], "]("); j > 0 {
				if e := strings.IndexByte(s[i+j:], ')'); e > 0 {
					b.WriteString(s[i+1 : i+j])
					i += j + e
					continue
				}
			}
		case c == ':':
			if name, e, ok := emojiCodeAt(s, i); ok {
				b.WriteString(e)
				i += len(name) + 1
				continue
			}
		case c == '*' || c == '_' || c == '~':
			j := i
			for j < len(s) && s[j] == c {
				j++
			}
			before, _ := utf8.DecodeLastRuneInString(s[:i])
			after, _ := utf8.DecodeRuneInString(s[j:])
			if i == 0 {
				before = ' '
			}
			if j == len(s) {
				after = ' '
			}
			left := !mdSpace(after) && (!mdPunct(after) || mdSpace(before) || mdPunct(before))
			right := !mdSpace(before) && (!mdPunct(before) || mdSpace(after) || mdPunct(after))
			open, close := left, right
			if c == '_' {
				open = left && (!right || mdPunct(before))
				close = right && (!left || mdPunct(after))
			}
			if c == '~' && j-i > 2 {
				open, close = false, false
			}
			flush()
			toks = append(toks, mdTok{delim: c, n: j - i, orig: j - i, open: open, close: close})
			i = j - 1
			continue
		}
		b.WriteByte(s[i])
	}
	flush()
	return toks
}

// mdEmphasis pairs delimiter runs as CommonMark's "process emphasis" does: each closer, in order, with the nearest
// opener of its character before it, the rule of 3 kept; ~ pairs only with a run of its own length.
func mdEmphasis(toks []mdTok) {
	active := make([]bool, len(toks))
	for i, t := range toks {
		active[i] = t.delim != 0
	}
	for ci := range toks {
		for active[ci] && toks[ci].close && toks[ci].n > 0 {
			cl := &toks[ci]
			oi := -1
			for k := ci - 1; k >= 0; k-- {
				op := &toks[k]
				if !active[k] || op.delim != cl.delim || !op.open || op.n == 0 {
					continue
				}
				if cl.delim == '~' && op.orig != cl.orig {
					continue
				}
				if cl.delim != '~' && (op.close || cl.open) && (op.orig+cl.orig)%3 == 0 && (op.orig%3 != 0 || cl.orig%3 != 0) {
					continue
				}
				oi = k
				break
			}
			if oi < 0 {
				if !cl.open {
					active[ci] = false
				}
				break
			}
			op := &toks[oi]
			use := 1
			if op.n >= 2 && cl.n >= 2 {
				use = 2
			}
			var st mdStyle
			switch {
			case cl.delim == '~':
				use, st.strike = cl.n, 1
			case use == 2:
				st.bold = 1
			default:
				st.it = 1
			}
			op.n, cl.n = op.n-use, cl.n-use
			op.opens.it, op.opens.bold, op.opens.strike = op.opens.it+st.it, op.opens.bold+st.bold, op.opens.strike+st.strike
			cl.closes.it, cl.closes.bold, cl.closes.strike = cl.closes.it+st.it, cl.closes.bold+st.bold, cl.closes.strike+st.strike
			for k := oi + 1; k < ci; k++ {
				active[k] = false
			}
			if op.n == 0 {
				active[oi] = false
			}
		}
	}
}

//go:embed gemoji/shortcodes.txt
var emojiCodes string // GitHub's emoji codes, "name emoji" a line, sorted (gemoji/SOURCE)

// emojiCodeAt reads a GitHub emoji code, :name:, at s[i], as GitHub writes it as its emoji: one of gemoji's names,
// not inside a word.
func emojiCodeAt(s string, i int) (name, emoji string, ok bool) {
	if i > 0 && mdWordByte(s[i-1]) {
		return "", "", false
	}
	j := i + 1
	for j < len(s) && (mdWordByte(s[j]) || s[j] == '_' || s[j] == '+' || s[j] == '-') {
		j++
	}
	if j == i+1 || j >= len(s) || s[j] != ':' || j+1 < len(s) && mdWordByte(s[j+1]) {
		return "", "", false
	}
	name = s[i+1 : j]
	at := len(name) + 1 // past "name " on the first line
	if !strings.HasPrefix(emojiCodes, name+" ") {
		k := strings.Index(emojiCodes, "\n"+name+" ")
		if k < 0 {
			return "", "", false
		}
		at += k + 1
	}
	emoji, _, _ = strings.Cut(emojiCodes[at:], "\n")
	return name, emoji, true
}

func mdWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// mdSpace is Unicode whitespace as CommonMark has it: Zs, tab, newline, form feed, carriage return.
func mdSpace(r rune) bool {
	return r == '\t' || r == '\n' || r == '\f' || r == '\r' || unicode.Is(unicode.Zs, r)
}

// mdPunct is a punctuation character as CommonMark has it: ASCII punctuation, or Unicode P.
func mdPunct(r rune) bool {
	return r < 0x80 && strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", r) || unicode.IsPunct(r)
}

// MarkdownOpts are where a document's relative images are, the lightbox group its images make (one of its own if
// ""), and whether a newline in a paragraph is a line break (a comment's) or a space (a document's: CommonMark's soft
// break).
type MarkdownOpts struct {
	Dir    string
	Group  string
	Help   string
	Breaks bool
}

// CommentMarkdown is how a comment draws its markdown, as GitHub draws a comment's: each newline a line break.
func CommentMarkdown() MarkdownOpts { return MarkdownOpts{Breaks: true} }

// MarkdownText draws src as the window's markdown service does, as o says (its images found from o.Dir, its newlines
// line breaks with o.Breaks), or wrapped as it is without one.
func MarkdownText(c *ui.Ctx, r registry.Registry, key any, src string, width float32, body ui.TextStyle, o MarkdownOpts) {
	if md, ok := registry.ServiceOf[Markdown](r, MarkdownService); ok {
		md.TextIn(c, key, src, width, body, o)
		return
	}
	widget.Wrap(c, key, src, width, body)
}

// DocText draws markdown as a doc reads, a hover's or an answer's: the UI face, muted.
func DocText(c *ui.Ctx, r registry.Registry, key any, src string, width float32) {
	MarkdownText(c, r, key, src, width, UIFont(12, c.Theme().C(theme.FgMuted)), MarkdownOpts{})
}

// PlainText is markdown as it reads, without its marks: headings, quotes, fences, emphasis, code ticks and links'
// targets go; list markers and line breaks stay.
func PlainText(md string) string {
	var out []string
	for _, ln := range strings.Split(md, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			continue
		}
		indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
		t = mdHeading.ReplaceAllString(t, "")
		t = strings.TrimPrefix(strings.TrimPrefix(t, ">"), " ")
		t = mdWholeImage.ReplaceAllString(t, "")
		var b strings.Builder
		for _, sp := range MDSpans(t) {
			b.WriteString(sp.Text)
		}
		out = append(out, indent+strings.TrimSpace(b.String()))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

var (
	mdHeading    = regexp.MustCompile(`^#{1,6}\s+`)
	mdWholeImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
)
