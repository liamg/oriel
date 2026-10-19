package app

import (
	"fmt"
	"image"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
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
	Images    []mdImage
	Align     string // "center" or "right" from its HTML, or ""
}

var listItem = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)

// mdParse splits markdown into blocks: headings, paragraphs, list items (with their continuation lines), fenced
// code, rules, and HTML blocks as the blocks they mean. Images come out of the text into each block's Images.
func mdParse(src string) []mdBlock {
	return mdImagesOut(mdBlocks(src))
}

func mdBlocks(src string) []mdBlock {
	lines := strings.Split(strings.TrimRight(src, " \t\r\n"), "\n")
	var out []mdBlock
	para := -1
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		t := strings.TrimSpace(l)
		if t != "" && para >= 0 && !strings.HasPrefix(t, "```") && t != "---" && t != "***" && !strings.HasPrefix(t, "#") && !listItem.MatchString(l) && !startsHTMLBlock(l) {
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
		case startsHTMLBlock(l):
			from := i
			comment := strings.HasPrefix(t, "<!--")
			for i+1 < len(lines) && (comment && !strings.Contains(strings.Join(lines[from:i+1], "\n"), "-->") || !comment && strings.TrimSpace(lines[i+1]) != "") {
				i++
			}
			out = append(out, htmlBlocks(strings.Join(lines[from:i+1], "\n"), from+1, i+1)...)
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

// mdImagesOut takes each block's images out of its text, makes its inline HTML markdown, and drops what's left empty.
func mdImagesOut(bs []mdBlock) []mdBlock {
	out := bs[:0]
	for _, b := range bs {
		if b.Kind == mdPara || b.Kind == mdHeading || b.Kind == mdItem {
			text, imgs := mdTakeImages(b.Text)
			b.Text, b.Images = mdInlineHTML(text), append(b.Images, imgs...)
			if b.Text == "" && len(b.Images) == 0 && b.Kind != mdItem {
				continue
			}
		}
		out = append(out, b)
	}
	return out
}

// markdownText draws markdown in body's style: headings, paragraphs and list items with inline code and bold,
// fenced code in mono, rules, and images (relative ones only as chips: markdownTextIn knows their folder).
func markdownText(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle) {
	markdownTextIn(c, key, src, width, body, mdOpts{})
}

// mdOpts are where a document's relative images are, and the lightbox group its images make (one of its own if "").
type mdOpts struct {
	Dir   string
	Group string
	Help  string
}

// markdownTextIn is markdownText with its images found from o.Dir.
func markdownTextIn(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle, o mdOpts) {
	th := c.Theme()
	code := ui.TextStyle{Font: codeMono, Color: th.C(theme.Fg)}
	head := body
	head.Font.Weight, head.Color = typeset.SemiBold, th.C(theme.Fg)
	if o.Group == "" {
		o.Group = fmt.Sprint("md:", c.Next(key))
	}
	c.Column(key, ui.Style{W: ui.Px(width), Gap: 8}, func(c *ui.Ctx) {
		for k, b := range mdParse(src) {
			if len(b.Images) > 0 {
				mdPictures(c, fmt.Sprint("img", k), b.Images, b.Align, width, o)
				if b.Text == "" && b.Kind != mdItem {
					continue
				}
			}
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
				mdAligned(c, fmt.Sprint("h", k), b.Align, width, func(c *ui.Ctx, key any) { widget.Rich(c, key, mdSpans(b.Text), width, head, widget.RichOpts{}) })
			case mdItem:
				indent := float32(b.Level) * 14
				c.Row(fmt.Sprint("li", k), ui.Style{Gap: 8, Pad: ui.Insets{L: 4 + indent}}, func(c *ui.Ctx) {
					c.Text("mark", b.Mark, body)
					widget.Rich(c, "t", mdSpans(b.Text), width-22-indent, body, widget.RichOpts{})
				})
			default:
				mdAligned(c, fmt.Sprint("p", k), b.Align, width, func(c *ui.Ctx, key any) { widget.Rich(c, key, mdSpans(b.Text), width, body, widget.RichOpts{}) })
			}
		}
	})
}

// threadMarkdown is how a thread's comments draw their markdown: images found from its project's folder (the primary
// project's, or the first, when it isn't anchored), all of them one lightbox group.
func threadMarkdown(se core.SessionState, t core.Thread) mdOpts {
	o := mdOpts{Group: "thread:" + string(t.ID), Help: "agent/requests#request"}
	for i, p := range se.Projects {
		if t.Anchor != nil && p.Name == t.Anchor.Project || t.Anchor == nil && p.Primary || o.Dir == "" && i == 0 {
			o.Dir = p.Dir
		}
	}
	return o
}

// mdAligned builds a block's text, aligned as its HTML says: centred or to the right, or as it falls.
func mdAligned(c *ui.Ctx, key any, align string, width float32, text func(c *ui.Ctx, key any)) {
	cross := ui.Start
	switch align {
	case "center":
		cross = ui.Center
	case "right":
		cross = ui.End
	default:
		text(c, key)
		return
	}
	c.Column(key, ui.Style{W: ui.Px(width), Cross: cross}, func(c *ui.Ctx) { text(c, "t") })
}

// mdPictures draws a block's images in rows that wrap at width, aligned as the block says: each one zoomable, in the
// document's lightbox group, at the size its attributes ask for and never wider than the column. One that can't be
// shown (a remote or SVG image, a missing file) is a chip with its alt text and source.
func mdPictures(c *ui.Ctx, key any, imgs []mdImage, align string, width float32, o mdOpts) {
	th := c.Theme()
	type pic struct {
		im  mdImage
		img image.Image
		why string
		w   float32
	}
	const gap = 8
	chipFont := uiFont(12, th.C(theme.FgMuted))
	srcFont := monoFont(10.5, th.C(theme.FgFaint))
	var rows [][]pic
	var used float32
	for _, im := range imgs {
		p := pic{im: im}
		p.img, p.why = mdPicture(im.Src, o.Dir)
		if p.img != nil {
			b := p.img.Bounds()
			w := float32(b.Dx())
			switch {
			case im.W > 0:
				w = im.W
			case im.WPct > 0:
				w = im.WPct * width
			case im.H > 0:
				w = im.H * float32(b.Dx()) / float32(max(b.Dy(), 1))
			}
			p.w = min(w, width)
		} else {
			p.w = min(14+c.Shaper().Shape(cmpOr(im.Alt, "image"), chipFont.Font).Advance+c.Shaper().Shape(im.Src, srcFont.Font).Advance+16+16, width, 320)
		}
		if len(rows) == 0 || used+gap+p.w > width {
			rows, used = append(rows, nil), p.w
		} else {
			used += gap + p.w
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], p)
	}
	main := ui.Start
	switch align {
	case "center":
		main = ui.Center
	case "right":
		main = ui.End
	}
	c.Column(key, ui.Style{W: ui.Px(width), Gap: gap}, func(c *ui.Ctx) {
		for r, row := range rows {
			c.Row(r, ui.Style{W: ui.Px(width), Gap: gap, Main: main, Cross: ui.End}, func(c *ui.Ctx) {
				for i, p := range row {
					if p.img != nil {
						widget.Image(c, i, p.img, p.w, widget.ImageOpts{Label: cmpOr(p.im.Alt, p.im.Src), Help: o.Help, Zoom: true, Group: o.Group})
						continue
					}
					c.Row(i, ui.Style{W: ui.Px(p.w), H: ui.Px(24), Pad: ui.PadXY(8, 0), Gap: 8, Cross: ui.Center, Radius: 4, Stroke: th.C(theme.LineStrong),
						Border: 1, Clip: true, Role: ui.RoleImage, Label: cmpOr(p.im.Alt, p.im.Src), Tooltip: p.why, Help: o.Help}, func(c *ui.Ctx) {
						fg := th.C(theme.FgFaint)
						c.Paint("icon", ui.Px(14), ui.Px(11), func(f render.Frame, r render.Rect) { paintPictureIcon(f, r, fg) })
						c.Text("alt", cmpOr(p.im.Alt, "image"), chipFont)
						c.Text("src", p.im.Src, srcFont)
					})
				}
			})
		}
	})
}

// paintPictureIcon draws a small picture, a frame with a hill in it, into r.
func paintPictureIcon(f render.Frame, r render.Rect, fg render.Color) {
	f.Rect(r, render.Style{Stroke: fg, Width: 1.2, Radius: 2})
	var p render.Path
	p.MoveTo(render.Point{X: r.Min.X + 2, Y: r.Max.Y - 2}).LineTo(render.Point{X: r.Min.X + r.Dx()*.45, Y: r.Min.Y + r.Dy()*.45}).
		LineTo(render.Point{X: r.Max.X - 2, Y: r.Max.Y - 2}).Close()
	f.Path(&p, render.Style{Fill: fg})
}

// mdPicture is the image a markdown src names: a file, relative to dir unless it's absolute, or a data: URI. Without
// one, why says why.
func mdPicture(src, dir string) (img image.Image, why string) {
	var err error
	switch {
	case src == "":
		return nil, "an image with no source"
	case strings.HasPrefix(src, "data:"):
		img, _, err = pictures.data(src)
	case strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "//"):
		return nil, "Remote images aren't fetched"
	default:
		p := strings.TrimPrefix(src, "file://")
		if i := strings.IndexAny(p, "?#"); i >= 0 {
			p = p[:i]
		}
		if u, err := url.PathUnescape(p); err == nil {
			p = u
		}
		if !filepath.IsAbs(p) {
			if dir == "" {
				return nil, "There's no folder to find " + src + " in"
			}
			p = filepath.Join(dir, filepath.FromSlash(p))
		}
		img, _, err = pictures.file(p)
	}
	if err != nil {
		return nil, err.Error()
	}
	return img, ""
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
