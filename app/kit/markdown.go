package kit

import (
	"regexp"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ThreadMarkdown is how a thread's comments draw their markdown: images found from its project's folder (the primary
// project's, or the first, when it isn't anchored), all of them one lightbox group.
func ThreadMarkdown(se core.SessionState, t core.Thread) MarkdownOpts {
	o := MarkdownOpts{Group: "thread:" + string(t.ID), Help: "agent/requests#request"}
	for i, p := range se.Projects {
		if t.Anchor != nil && p.Name == t.Anchor.Project || t.Anchor == nil && p.Primary || o.Dir == "" && i == 0 {
			o.Dir = p.Dir
		}
	}
	return o
}

// MDSpans is a paragraph's inline markdown as spans: `code`, **bold**, ~~struck~~, and [links](url) as their text.
func MDSpans(s string) []widget.Span {
	var out []widget.Span
	var b strings.Builder
	bold, strike := false, false
	emit := func() {
		if b.Len() > 0 {
			out = append(out, widget.Span{Text: b.String(), Bold: bold, Strike: strike})
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '`':
			if j := strings.IndexByte(s[i+1:], '`'); j >= 0 {
				emit()
				out = append(out, widget.Span{Text: s[i+1 : i+1+j], Code: true})
				i += j + 1
				continue
			}
		case strings.HasPrefix(s[i:], "**") || strings.HasPrefix(s[i:], "__"):
			emit()
			bold = !bold
			i++
			continue
		case strings.HasPrefix(s[i:], "~~"):
			emit()
			strike = !strike
			i++
			continue
		case s[i] == '[':
			if j := strings.Index(s[i:], "]("); j > 0 {
				if e := strings.IndexByte(s[i+j:], ')'); e > 0 {
					b.WriteString(s[i+1 : i+j])
					i += j + e
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	emit()
	return out
}

// MarkdownOpts are where a document's relative images are, and the lightbox group its images make (one of its own if
// "").
type MarkdownOpts struct {
	Dir   string
	Group string
	Help  string
}

// MarkdownTextIn is MarkdownText with its images found from o.Dir.
func MarkdownTextIn(c *ui.Ctx, r registry.Registry, key any, src string, width float32, body ui.TextStyle, o MarkdownOpts) {
	if md, ok := registry.ServiceOf[Markdown](r, MarkdownService); ok {
		md.TextIn(c, key, src, width, body, o)
		return
	}
	widget.Wrap(c, key, src, width, body)
}

// DocText draws markdown as a doc reads, a hover's or an answer's: the UI face, muted.
func DocText(c *ui.Ctx, r registry.Registry, key any, src string, width float32) {
	MarkdownText(c, r, key, src, width, UIFont(12, c.Theme().C(theme.FgMuted)))
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
