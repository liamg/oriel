package editor

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tMdMark       = theme.Declare("markdown.mark", "amber")
	tMdCodeBorder = theme.Declare("markdown.code.border", "line.strong")
)

// RemoteImagesOption turns loading markdown's remote images on or off.
const RemoteImagesOption = "markdown.remote_images"

// Markdown is the built-in markdown preview: a presenter drawing a markdown buffer as a document with the shared
// renderer's blocks (mdParse), its Preview / Edit action, and :MarkdownPreview. It's written as a plugin would be.
type Markdown struct {
	kit.Disposer
	Pictures *kit.Pictures // the window's
}

func (x *Markdown) Manifest() plugin.Manifest { return plugin.Manifest{ID: "markdown"} }

// Text draws src as markdown, wrapped to width (kit.Markdown).
func (x *Markdown) Text(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle) {
	markdownText(c, key, src, width, body)
}

// Title is src's first level-1 heading, as plain text (kit.Markdown).
func (x *Markdown) Title(src string) (string, bool) {
	for _, b := range mdParse(src) {
		if b.Kind == mdHeading && b.Level == 1 {
			return core.PlainMarkdown(b.Text), true
		}
	}
	return "", false
}

// TextIn is Text with its images found from o.Dir (kit.Markdown).
func (x *Markdown) TextIn(c *ui.Ctx, key any, src string, width float32, body ui.TextStyle, o kit.MarkdownOpts) {
	markdownTextIn(c, key, src, width, body, o)
}

func isMarkdown(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

func (x *Markdown) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Service{Name: kit.MarkdownService, Impl: x, Source: "markdown"}))
	x.Add(h.Contribute(uireg.BufferPresenter{Name: "markdown", Label: "Preview", When: isMarkdown, Comments: true, Build: markdownBlocks,
		Help: "editor/pane#present"}))
	x.Add(h.Contribute(registry.EditorAction{Name: "markdown.preview", Label: "Preview", LabelOn: "Edit", Command: "editor.present",
		Args: registry.Args{"name": "markdown"}, When: isMarkdown, On: func(w registry.EditorWindow) bool { return w.Presenter == "markdown" },
		Order: 5, Help: "editor/pane#present"}))
	x.Add(h.Option(registry.OptionSpec{Name: RemoteImagesOption, Type: registry.Bool, Default: true,
		Scopes: registry.ScopeUser, Help: "Load the images markdown links from the web (http and https). Off, each is a chip with its address."}))
	remoteImages := func() {
		on, _, _ := h.Options().Get(RemoteImagesOption, registry.At{})
		x.Pictures.SetRemote(on == true)
	}
	remoteImages()
	x.Add(h.Options().Watch(RemoteImagesOption, remoteImages))
	x.Add(registry.DisposeFunc(func() { x.Pictures.SetRemote(true) }))
	x.Add(h.Command(registry.Command{ID: "MarkdownPreview", Title: "Preview the markdown file in the focused window, or edit it again",
		Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "editor.present", registry.Args{"name": "markdown"})
		}}))
	return nil
}

// markdownBlocks maps each markdown block to its source lines.
func markdownBlocks(ctx *uireg.PresentCtx) {
	md := mdParse(strings.Join(ctx.Lines, "\n"))
	o := mdOpts{Dir: filepath.Dir(ctx.Path), Group: "md:" + ctx.Path, Help: "editor/pane#present"}
	first := slices.IndexFunc(md, func(b mdBlock) bool { return b.Kind == mdHeading && b.Level == 1 })
	var toc []string
	for _, b := range md {
		if b.Kind == mdHeading && b.Level == 2 {
			toc = append(toc, core.PlainMarkdown(b.Text))
		}
	}
	for i, b := range md {
		line := ""
		if b.Line-1 < len(ctx.Lines) {
			line = ctx.Lines[b.Line-1]
		}
		pb := ctx.Block(b.Line-1, b.End-1, func(c *ui.Ctx, d uireg.PresentDraw) {
			markdownBlock(c, b, line, i == first, toc, d, o)
		})
		pb.Space, pb.Label = 6, core.PlainMarkdown(b.Text)
		if b.Kind == mdTable {
			pb.Label = core.PlainMarkdown(strings.Join(b.Rows[0], " · "))
		}
		if pb.Label == "" && len(b.Images) > 0 {
			pb.Label = cmp.Or(b.Images[0].Alt, b.Images[0].Src)
		}
		if b.Kind == mdHeading && b.Level > 1 {
			pb.Space = 22
		}
	}
}

// markdownBlock draws a block as a document does: headings large (the first title with the document's label over
// it and its sections under it), a fence in a box (a mermaid one as its diagram), a one-line block that changed as
// what it was and what it is.
func markdownBlock(c *ui.Ctx, b mdBlock, line string, title bool, toc []string, d uireg.PresentDraw, o mdOpts) {
	if mdDiagramBox(c, "diagram", b, d.Width, o, true) {
		return
	}
	if len(b.Images) == 0 {
		markdownBody(c, b, line, title, toc, d)
		return
	}
	c.Column("pics", ui.Style{W: ui.Px(d.Width), Gap: 10}, func(c *ui.Ctx) {
		mdPictures(c, "img", b.Images, b.Align, d.Width, o)
		if b.Text != "" || b.Kind == mdItem {
			markdownBody(c, b, line, title, toc, d)
		}
	})
}

// markdownBody is a block's text.
func markdownBody(c *ui.Ctx, b mdBlock, line string, title bool, toc []string, d uireg.PresentDraw) {
	th := c.Theme()
	width := d.Width
	body := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 14.5}, Color: th.C(theme.Fg)}
	spans := kit.MDSpans(b.Text)
	if b.Line == b.End {
		for _, dc := range d.Decorations {
			if dc.Kind == uireg.LineBg && dc.Was != "" {
				spans = wordDiff(dc.Was, line, b)
				break
			}
		}
	}
	switch b.Kind {
	case mdHeading:
		if b.Level == 1 {
			cross := ui.Start
			if b.Align == "center" {
				cross = ui.Center
			}
			c.Column("h", ui.Style{W: ui.Px(width), Gap: 10, Pad: ui.Insets{B: 8}, Cross: cross}, func(c *ui.Ctx) {
				if title && d.Kicker != "" {
					c.Text("k", strings.ToUpper(d.Kicker), kit.KickerFont(th.C(tMdMark)))
				}
				mdAligned(c, "t", b.Align, width, func(c *ui.Ctx, key any) {
					widget.Rich(c, key, spans, width, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 28, Weight: typeset.Medium},
						Color: th.C(theme.Fg)}, widget.RichOpts{})
				})
				if len(toc) > 0 && title {
					c.Text("toc", strings.Join(toc, " · "), kit.MonoFont(10.5, th.C(theme.FgFaint)))
				}
			})
			return
		}
		c.Row("h", ui.Style{Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dash", ui.Style{W: ui.Px(14), H: ui.Px(2), Fill: th.C(tMdMark)}, nil)
			widget.Rich(c, "t", spans, width-26, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 17, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)}, widget.RichOpts{})
		})
	case mdItem:
		indent := float32(b.Level) * 16
		c.Row("li", ui.Style{Gap: 10, Pad: ui.Insets{L: indent}}, func(c *ui.Ctx) {
			c.Text("mark", b.Mark, kit.UIFont(14.5, th.C(theme.FgFaint)))
			widget.Rich(c, "t", spans, width-24-indent, body, widget.RichOpts{})
		})
	case mdCode:
		indent := mdCodeIndent(b, 16)
		c.Row("code", ui.Style{W: ui.Px(width), Pad: ui.Insets{L: indent}}, func(c *ui.Ctx) {
			c.Column("box", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 3, Radius: 6, Stroke: th.C(tMdCodeBorder), Border: 1, Fill: th.C(theme.BgSurface)},
				func(c *ui.Ctx) {
					if b.Lang != "" {
						c.Text("lang", mdFenceKicker(c, b), kit.KickerFont(th.C(theme.FgFaint)))
					}
					widget.WrapCode(c, "lines", strings.Join(b.Code, "\n"), width-indent-28, 3, kit.MonoFont(12, th.C(theme.FgMuted)))
				})
		})
	case mdRule:
		c.Box("hr", ui.Style{W: ui.Px(width), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	case mdTable:
		var part func(int, ui.ID)
		if d.Part != nil {
			part = func(line int, id ui.ID) { d.Part(line, line, id) }
		}
		markdownTable(c, "table", b, width, body, part)
	default:
		mdAligned(c, "p", b.Align, width, func(c *ui.Ctx, key any) { widget.Rich(c, key, spans, width, body, widget.RichOpts{}) })
	}
}

// wordDiff is a one-line block's text with what the edit took out struck through, the words both share kept: the
// middle words that differ, old then new.
func wordDiff(was, now string, b mdBlock) []widget.Span {
	strip := func(s string) string {
		if b.Kind == mdItem {
			if m := listItem.FindStringSubmatch(s); m != nil {
				return m[3]
			}
		}
		return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "#"))
	}
	ow, nw := strings.Fields(strip(was)), strings.Fields(strip(now))
	p := 0
	for p < len(ow) && p < len(nw) && ow[p] == nw[p] {
		p++
	}
	s := 0
	for s < len(ow)-p && s < len(nw)-p && ow[len(ow)-1-s] == nw[len(nw)-1-s] {
		s++
	}
	var out []widget.Span
	add := func(words []string) {
		if len(words) > 0 {
			out = append(out, kit.MDSpans(strings.Join(words, " ")+" ")...)
		}
	}
	add(nw[:p])
	if old := ow[p : len(ow)-s]; len(old) > 0 {
		out = append(out, widget.Span{Text: core.PlainMarkdown(strings.Join(old, " ")), Strike: true}, widget.Span{Text: " "})
	}
	add(nw[p : len(nw)-s])
	add(nw[len(nw)-s:])
	return out
}
