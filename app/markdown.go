package app

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// mdKind is what a markdown block is.
type mdKind int

const (
	mdPara mdKind = iota
	mdHeading
	mdItem
	mdCode
	mdRule
)

// mdBlock is one block of markdown and the source lines it came from, so a thread can be anchored to it.
type mdBlock struct {
	Kind      mdKind
	Text      string   // a paragraph's, heading's or item's inline markdown
	Level     int      // a heading's level; an item's depth
	Mark      string   // an item's bullet, or its number
	Lang      string   // a fence's info string
	Code      []string // a fence's lines
	Line, End int      // 1-based, in the source
}

var listItem = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)

// mdParse splits markdown into blocks: headings, paragraphs, list items (with their continuation lines), fenced
// code and rules.
func mdParse(src string) []mdBlock {
	lines := strings.Split(strings.TrimRight(src, " \t\r\n"), "\n")
	var out []mdBlock
	para := -1
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		t := strings.TrimSpace(l)
		if t != "" && para >= 0 && !strings.HasPrefix(t, "```") && t != "---" && t != "***" && !strings.HasPrefix(t, "#") && !listItem.MatchString(l) {
			out[para].Text += " " + t
			out[para].End = i + 1
			continue
		}
		para = -1
		switch {
		case strings.HasPrefix(t, "```"):
			b := mdBlock{Kind: mdCode, Lang: strings.TrimSpace(strings.TrimPrefix(t, "```")), Line: i + 1}
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				b.Code = append(b.Code, strings.TrimRight(lines[i], "\r"))
			}
			b.End = min(i+1, len(lines))
			out = append(out, b)
		case t == "---" || t == "***":
			out = append(out, mdBlock{Kind: mdRule, Line: i + 1, End: i + 1})
		case t == "":
		case strings.HasPrefix(t, "#"):
			level := len(t) - len(strings.TrimLeft(t, "#"))
			out = append(out, mdBlock{Kind: mdHeading, Level: level, Text: strings.TrimLeft(strings.TrimLeft(t, "#"), " "), Line: i + 1, End: i + 1})
		case listItem.MatchString(l):
			m := listItem.FindStringSubmatch(l)
			b := mdBlock{Kind: mdItem, Text: m[3], Level: len(m[1]) / 2, Mark: "•", Line: i + 1, End: i + 1}
			if m[2][0] >= '0' && m[2][0] <= '9' {
				b.Mark = m[2]
			}
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") && !listItem.MatchString(lines[i+1]) && strings.TrimSpace(lines[i+1]) != "" {
				i++
				b.Text += " " + strings.TrimSpace(lines[i])
				b.End = i + 1
			}
			out = append(out, b)
		default:
			out = append(out, mdBlock{Kind: mdPara, Text: t, Line: i + 1, End: i + 1})
			para = len(out) - 1
		}
	}
	return out
}

// markdownText draws markdown in body's style: headings, paragraphs and list items with inline code and bold,
// fenced code in mono, and rules.
func markdownText(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle) {
	th := c.Theme()
	code := ui.TextStyle{Font: codeMono, Color: th.C(theme.Fg)}
	head := body
	head.Font.Weight, head.Color = typeset.SemiBold, th.C(theme.Fg)
	c.Column(key, ui.Style{W: ui.Px(width), Gap: 8}, func(c *ui.Ctx) {
		for k, b := range mdParse(src) {
			switch b.Kind {
			case mdCode:
				c.Column(fmt.Sprint("code", k), ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 6), Radius: 4, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
					for j, l := range b.Code {
						c.Text(j, strings.ReplaceAll(l, "\t", "    "), code)
					}
				})
			case mdRule:
				c.Box(fmt.Sprint("hr", k), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			case mdHeading:
				widget.Rich(c, fmt.Sprint("h", k), mdSpans(b.Text), width, head, widget.RichOpts{})
			case mdItem:
				indent := float32(b.Level) * 14
				c.Row(fmt.Sprint("li", k), ui.Style{Gap: 8, Pad: ui.Insets{L: 4 + indent}}, func(c *ui.Ctx) {
					c.Text("mark", b.Mark, body)
					widget.Rich(c, "t", mdSpans(b.Text), width-22-indent, body, widget.RichOpts{})
				})
			default:
				widget.Rich(c, fmt.Sprint("p", k), mdSpans(b.Text), width, body, widget.RichOpts{})
			}
		}
	})
}

// mdSpans is a paragraph's inline markdown as spans: `code`, **bold**, ~~struck~~, and [links](url) as their text.
func mdSpans(s string) []widget.Span {
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
