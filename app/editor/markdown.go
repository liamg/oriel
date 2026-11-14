package editor

import (
	"cmp"
	"fmt"
	"image"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/svg"
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
	mdTable
)

// mdBlock is one block of markdown and the source lines it came from, so a thread can be anchored to it.
type mdBlock struct {
	Kind      mdKind
	Text      string   // a paragraph's, heading's or item's inline markdown, its source's newlines kept
	Level     int      // a heading's level; an item's depth; a fence's in a list item, its item's depth + 1
	Mark      string   // an item's bullet, or its number
	Lang      string   // a fence's info string
	Code      []string // a fence's lines
	Line, End int      // 1-based, in the source
	Images    []mdImage
	Align     string     // "center" or "right" from its HTML, or ""
	Rows      [][]string // a table's cells' inline markdown, its header first, each row as wide as the header
	Aligns    []string   // a table's columns' alignment: "left", "center", "right" or ""
}

var listItem = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)

// mdParse splits markdown into blocks: headings, paragraphs, list items (with their continuation lines), fenced
// code, rules, GFM tables, and HTML blocks as the blocks they mean. Images come out of the text into each block's Images.
func mdParse(src string) []mdBlock {
	return mdImagesOut(mdBlocks(src))
}

// mdDocs are a UI's markdown sources parsed, the most recently drawn kept.
type mdDocs struct{ *work.Memo[string, []mdBlock] }

// mdParsed is src's blocks, parsed once while it's drawn.
func mdParsed(c *ui.Ctx, src string) []mdBlock {
	docs := ui.Keep(c, func() mdDocs { return mdDocs{work.NewMemo[string, []mdBlock](256)} })
	return docs.Get(src, func() []mdBlock { return mdParse(src) })
}

func mdBlocks(src string) []mdBlock {
	lines := strings.Split(strings.TrimRight(src, " \t\r\n"), "\n")
	var out []mdBlock
	para := -1
	for i := 0; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r")
		t := strings.TrimSpace(l)
		if t != "" && para >= 0 && !mdImageLine(t) && !strings.HasPrefix(t, "```") && t != "---" && t != "***" && !strings.HasPrefix(t, "#") && !listItem.MatchString(l) && !startsHTMLBlock(l) && mdTableAt(lines, i) == nil {
			out[para].Text += "\n" + t
			out[para].End = i + 1
			continue
		}
		para = -1
		switch {
		case strings.HasPrefix(t, "```"):
			var b mdBlock
			b, i = mdFence(lines, i, out)
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
			// GFM's task list items: "- [ ] todo", "- [x] done".
			if box, rest, ok := taskBox(b.Text); ok {
				b.Mark, b.Text = box, rest
			}
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "  ") && !listItem.MatchString(lines[i+1]) && strings.TrimSpace(lines[i+1]) != "" &&
				!strings.HasPrefix(strings.TrimSpace(lines[i+1]), "```") {
				i++
				b.Text += "\n" + strings.TrimSpace(lines[i])
				b.End = i + 1
			}
			out = append(out, b)
		case mdTableAt(lines, i) != nil:
			b := mdBlock{Kind: mdTable, Rows: [][]string{mdRow(t)}, Aligns: mdTableAt(lines, i), Line: i + 1}
			for i += 2; i < len(lines) && !mdEndsTable(lines[i]); i++ {
				row := mdRow(strings.TrimSpace(lines[i]))
				b.Rows = append(b.Rows, append(row, make([]string, len(b.Aligns))...)[:len(b.Aligns)])
			}
			i--
			b.End = i + 1
			out = append(out, b)
		default:
			out = append(out, mdBlock{Kind: mdPara, Text: t, Line: i + 1, End: i + 1})
			if !mdImageLine(t) { // a line of images stands alone, where it's written
				para = len(out) - 1
			}
		}
	}
	return out
}

// mdFence is the fenced code starting at line i, and the line it ends on. Its lines lose as much of the fence's
// indent as they have; a fence indented under a list item (after its blocks) is that item's.
func mdFence(lines []string, i int, before []mdBlock) (mdBlock, int) {
	l := strings.TrimRight(lines[i], "\r")
	t := strings.TrimSpace(l)
	indent := len(l) - len(strings.TrimLeft(l, " "))
	b := mdBlock{Kind: mdCode, Lang: strings.TrimSpace(strings.TrimPrefix(t, "```")), Line: i + 1}
	if n := len(before); indent >= 2 && n > 0 && (before[n-1].Kind == mdItem || before[n-1].Kind == mdCode && before[n-1].Level > 0) {
		b.Level = before[n-1].Level
		if before[n-1].Kind == mdItem {
			b.Level++
		}
	}
	for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
		ln := strings.TrimRight(lines[i], "\r")
		b.Code = append(b.Code, ln[min(indent, len(ln)-len(strings.TrimLeft(ln, " "))):])
	}
	b.End = min(i+1, len(lines))
	return b, i
}

var tableDelim = regexp.MustCompile(`^:?-+:?$`)

// mdTableAt is the alignment of each column of the table starting at line i, or nil if none does: a row of cells with
// a delimiter row of as many under it.
func mdTableAt(lines []string, i int) []string {
	if i+1 >= len(lines) || !mdHasPipe(lines[i]) || !mdHasPipe(lines[i+1]) {
		return nil
	}
	delim := mdRow(strings.TrimSpace(lines[i+1]))
	if len(delim) != len(mdRow(strings.TrimSpace(lines[i]))) {
		return nil
	}
	aligns := make([]string, len(delim))
	for j, d := range delim {
		if !tableDelim.MatchString(d) {
			return nil
		}
		switch l, r := d[0] == ':', d[len(d)-1] == ':'; {
		case l && r:
			aligns[j] = "center"
		case r:
			aligns[j] = "right"
		case l:
			aligns[j] = "left"
		}
	}
	return aligns
}

// mdEndsTable is whether a line ends a table: it's blank or starts another block.
func mdEndsTable(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "```") || t == "---" || t == "***" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ">") ||
		listItem.MatchString(l) || startsHTMLBlock(l)
}

func mdHasPipe(l string) bool { return len(mdPipes(l)) > 0 }

// mdPipes are where l's unescaped pipes are.
func mdPipes(l string) []int {
	var at []int
	for i := 0; i < len(l); i++ {
		switch l[i] {
		case '\\':
			i++
		case '|':
			at = append(at, i)
		}
	}
	return at
}

// mdRow is a table row's cells, split at its unescaped pipes (a leading and a trailing one are optional), trimmed,
// with their escaped pipes unescaped.
func mdRow(t string) []string {
	pipes := mdPipes(t)
	from, to := 0, len(t)
	if len(pipes) > 0 && pipes[0] == 0 {
		from, pipes = 1, pipes[1:]
	}
	if len(pipes) > 0 && pipes[len(pipes)-1] == len(t)-1 && len(t)-1 >= from {
		to, pipes = len(t)-1, pipes[:len(pipes)-1]
	}
	var cells []string
	for _, p := range append(pipes, to) {
		cells = append(cells, strings.ReplaceAll(strings.TrimSpace(t[from:p]), `\|`, "|"))
		from = p + 1
	}
	return cells
}

// mdImagesOut takes each block's images out of its text, makes its inline HTML markdown, and drops what's left empty.
// A paragraph's images stay where they're written: it's split into its text and its images, in order.
func mdImagesOut(bs []mdBlock) []mdBlock {
	var out []mdBlock
	for _, b := range bs {
		if b.Kind == mdPara {
			for _, part := range mdSplitImages(b.Text) {
				p := b
				p.Text, p.Images = mdInlineHTML(part.text), append(slices.Clone(b.Images), part.images...)
				if p.Text != "" || len(p.Images) > 0 {
					out = append(out, p)
				}
			}
			continue
		}
		if b.Kind == mdHeading || b.Kind == mdItem {
			text, imgs := mdTakeImages(b.Text)
			b.Text, b.Images = mdInlineHTML(text), append(b.Images, imgs...)
			if b.Text == "" && len(b.Images) == 0 && b.Kind != mdItem {
				continue
			}
		}
		for _, row := range b.Rows {
			for j, cell := range row {
				text, imgs := mdTakeImages(cell)
				if text == "" && len(imgs) > 0 {
					text = imgs[0].Alt
				}
				row[j] = mdInlineHTML(text)
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

type mdOpts = kit.MarkdownOpts

// markdownTextIn is markdownText with its images found from o.Dir.
func markdownTextIn(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle, o mdOpts) {
	th := c.Theme()
	code := ui.TextStyle{Font: kit.CodeMono, Color: th.C(theme.Fg)}
	head := body
	head.Font.Weight, head.Color = typeset.SemiBold, th.C(theme.Fg)
	if o.Group == "" {
		o.Group = fmt.Sprint("md:", c.Next(key))
	}
	prose := widget.RichOpts{Breaks: o.Breaks}
	c.Column(key, ui.Style{W: ui.Px(width), Gap: 8, Selectable: true}, func(c *ui.Ctx) {
		for k, b := range mdParsed(c, src) {
			if len(b.Images) > 0 {
				mdPictures(c, fmt.Sprint("img", k), b.Images, b.Align, width, o)
				if b.Text == "" && b.Kind != mdItem {
					continue
				}
			}
			switch b.Kind {
			case mdCode:
				if mdDiagramBox(c, fmt.Sprint("code", k), b, width, o, false) {
					continue
				}
				indent := mdCodeIndent(b, 14)
				c.Row(fmt.Sprint("code", k), ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: indent}}, func(c *ui.Ctx) {
					c.Column("box", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(8, 6), Radius: 4, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
						widget.WrapCode(c, "lines", strings.Join(b.Code, "\n"), width-indent-16, 0, code)
					})
				})
			case mdRule:
				widget.Rule(c, fmt.Sprint("hr", k))
			case mdTable:
				markdownTable(c, fmt.Sprint("table", k), b, width, body, nil)
			case mdHeading:
				mdAligned(c, fmt.Sprint("h", k), b.Align, width, func(c *ui.Ctx, key any) { widget.Rich(c, key, kit.MDSpans(b.Text), width, head, widget.RichOpts{}) })
			case mdItem:
				indent := float32(b.Level) * 14
				c.Row(fmt.Sprint("li", k), ui.Style{Gap: 8, Pad: ui.Insets{L: 4 + indent}}, func(c *ui.Ctx) {
					c.Text("mark", b.Mark, body)
					widget.Rich(c, "t", kit.MDSpans(b.Text), width-22-indent, body, prose)
				})
			default:
				mdAligned(c, fmt.Sprint("p", k), b.Align, width, func(c *ui.Ctx, key any) { widget.Rich(c, key, kit.MDSpans(b.Text), width, body, prose) })
			}
		}
	})
}

// mdCodeIndent is how far a fence sits in, under its list item's text when it's in one: step a level of list.
func mdCodeIndent(b mdBlock, step float32) float32 {
	if b.Level == 0 {
		return 0
	}
	return 22 + float32(b.Level-1)*step
}

// markdownTable draws a table block: its cells' inline markdown in body, its header row semibold, each column aligned
// as its delimiter row says. part, if not nil, is told each row's node and its 0-based source line.
func markdownTable(c *ui.Ctx, key any, b mdBlock, width float32, body ui.TextStyle, part func(line int, id ui.ID)) {
	rows := make([][][]widget.Span, len(b.Rows))
	for r, row := range b.Rows {
		for _, cell := range row {
			rows[r] = append(rows[r], kit.MDSpans(cell))
		}
	}
	o := widget.TableOpts{Head: true, Label: "table"}
	if part != nil {
		o.Row = func(r int, id ui.ID) {
			line := b.Line - 1 // the header's; the delimiter row's is under it
			if r > 0 {
				line = b.Line + r
			}
			part(line, id)
		}
	}
	for _, a := range b.Aligns {
		al := ui.Start
		switch a {
		case "center":
			al = ui.Center
		case "right":
			al = ui.End
		}
		o.Align = append(o.Align, al)
	}
	widget.Table(c, key, rows, width, body, o)
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
// document's lightbox group, at the size its attributes ask for and never wider than the column. A remote one is a
// placeholder while it loads; one that can't be shown (a missing file, one that won't decode) is a chip with its alt
// text and source.
func mdPictures(c *ui.Ctx, key any, imgs []mdImage, align string, width float32, o mdOpts) {
	th := c.Theme()
	type pic struct {
		im      mdImage
		img     image.Image
		doc     *svg.Doc
		loading bool
		why     string
		retry   string // a failed remote one's URL, fetched again on a click
		w       float32
	}
	const gap = 8
	chipFont := kit.UIFont(12, th.C(theme.FgMuted))
	srcFont := kit.MonoFont(10.5, th.C(theme.FgFaint))
	var rows [][]pic
	var used float32
	for _, im := range imgs {
		p := pic{im: im}
		pc, why := mdPicture(kit.PicturesOf(c), im.Src, o.Dir)
		p.img, p.doc, p.loading, p.why = pc.Img, pc.Doc, pc.Loading, why
		if pc.Err != nil && pc.Src != "" {
			p.retry = pc.Src
		}
		if !pc.Retry.IsZero() {
			c.WakeAt(pc.Retry)
		}
		var nat render.Point
		switch {
		case p.img != nil:
			b, d := p.img.Bounds(), max(pc.Density, 1)
			nat = render.Point{X: float32(b.Dx()) / d, Y: float32(b.Dy()) / d}
		case p.doc != nil:
			nat = p.doc.Size
		}
		if nat.X > 0 {
			w := nat.X
			switch {
			case im.W > 0:
				w = im.W
			case im.WPct > 0:
				w = im.WPct * width
			case im.H > 0:
				w = im.H * nat.X / max(nat.Y, 1)
			}
			p.w = min(w, width)
		} else {
			p.w = min(14+c.Shaper().Shape(cmp.Or(im.Alt, "image"), chipFont.Font).Advance+c.Shaper().Shape(im.Src, srcFont.Font).Advance+16+16, width, 320)
			if p.loading {
				p.w = min(p.w+mdLoadingDots+8, width)
			}
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
					label := cmp.Or(p.im.Alt, p.im.Src)
					switch {
					case p.img != nil:
						widget.Image(c, i, p.img, p.w, widget.ImageOpts{Label: label, Help: o.Help, Zoom: true, Group: o.Group})
						continue
					case p.doc != nil:
						doc, sh := p.doc, c.Shaper()
						v := widget.Vector{Size: doc.Size, Fill: th.C(theme.BgSurface), Draw: func(f render.Frame, r render.Rect) { doc.Draw(f, r, sh) }}
						widget.VectorImage(c, i, v, p.w, widget.ImageOpts{Label: label, Help: o.Help, Zoom: true, Group: o.Group})
						continue
					}
					tip := p.why
					if p.loading {
						tip = "Loading " + p.im.Src
					}
					st := ui.Style{Dir: ui.Row, W: ui.Px(p.w), H: ui.Px(24), Pad: ui.PadXY(8, 0), Gap: 8, Cross: ui.Center, Radius: 4,
						Stroke: th.C(theme.LineStrong), Border: 1, Clip: true, Role: ui.RoleImage, Label: label, Tooltip: tip, Help: o.Help}
					chip := func(c *ui.Ctx) {
						fg := th.C(theme.FgFaint)
						c.Paint("icon", ui.Px(14), ui.Px(11), func(f render.Frame, r render.Rect) { paintPictureIcon(f, r, fg) })
						c.Text("alt", cmp.Or(p.im.Alt, "image"), chipFont)
						if p.loading {
							widget.Dots(c, "loading", widget.DotsOpts{Size: 4, Tone: fg, Name: "Loading " + label})
						}
						c.Text("src", p.im.Src, srcFont)
					}
					if p.retry == "" {
						c.Box(i, st, chip)
						continue
					}
					st.Role, st.Tooltip = ui.RoleNone, tip+" · Click to try again"
					if widget.Clickable(c, i, st, chip).Clicked {
						kit.PicturesOf(c).RetryNow(p.retry)
					}
				}
			})
		}
	})
}

// mdLoadingDots is the width of a loading placeholder's dots.
const mdLoadingDots = 4*3 + 3.2*2

// paintPictureIcon draws a small picture, a frame with a hill in it, into r.
func paintPictureIcon(f render.Frame, r render.Rect, fg render.Color) {
	f.Rect(r, render.Style{Stroke: fg, Width: 1.2, Radius: 2})
	var p render.Path
	p.MoveTo(render.Point{X: r.Min.X + 2, Y: r.Max.Y - 2}).LineTo(render.Point{X: r.Min.X + r.Dx()*.45, Y: r.Min.Y + r.Dy()*.45}).
		LineTo(render.Point{X: r.Max.X - 2, Y: r.Max.Y - 2}).Close()
	f.Path(&p, render.Style{Fill: fg})
}

// mdPicture is the image or drawing a markdown src names: a file, relative to dir unless it's absolute, a data: URI,
// or an http(s) URL, fetched in the background and loading until it's in. Without one, why says why.
func mdPicture(pics *kit.Pictures, src, dir string) (pc *kit.Picture, why string) {
	switch {
	case src == "":
		return &kit.Picture{}, "an image with no source"
	case strings.HasPrefix(src, "data:"):
		pc = pics.FromDataURI(src)
	case strings.HasPrefix(src, "//"):
		pc = pics.Remote("https:" + src)
	case strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"):
		pc = pics.Remote(src)
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
				return &kit.Picture{}, "There's no folder to find " + src + " in"
			}
			p = filepath.Join(dir, filepath.FromSlash(p))
		}
		pc = pics.File(p)
	}
	if pc.Err != nil {
		return pc, pc.Err.Error()
	}
	return pc, ""
}

// taskBox reads a task list item's box: ○ for "[ ] ", ✓ for "[x] ", and the text after it.
func taskBox(text string) (box, rest string, ok bool) {
	switch {
	case strings.HasPrefix(text, "[ ] "):
		return "○", text[4:], true
	case strings.HasPrefix(text, "[x] "), strings.HasPrefix(text, "[X] "):
		return "✓", text[4:], true
	}
	return "", text, false
}
