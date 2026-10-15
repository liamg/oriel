package tour

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"fmt"
	"html"
	"math"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Span is a run of a code line, coloured by its Class.
type Span struct {
	Text  string
	Class string
}

// HTMLOptions are what the export asks its caller: how code is coloured, and the colours.
type HTMLOptions struct {
	// Highlight colours lines start through end of a file's src (path "" for an example, named by lang). Each
	// span's Class is styled by Palette. Nil, or nil back, leaves the code plain.
	Highlight func(path, lang string, src []byte, start, end int) [][]Span
	// Palette is the page's colours: Palette's output, or CSS defining the same variables and classes.
	Palette string
}

// Markdown is the tour as one markdown document, every quote resolved to its code. Diagrams stay mermaid fences, and
// a screenshot's annotations become the numbered list they read as.
func Markdown(t Tour) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n", cmp.Or(t.Title, t.Name))
	if t.Kind != "" {
		fmt.Fprintf(&b, "_%s tour", t.Kind)
		for _, r := range t.Repos {
			fmt.Fprintf(&b, " · %s", r.Name)
			if r.Base != "" && r.Commit != "" {
				fmt.Fprintf(&b, " @ %s %s", r.Base, shortSHA(r.Commit))
			}
		}
		b.WriteString("_\n\n")
	}
	for i, s := range t.Steps {
		if i > 0 {
			b.WriteString("---\n\n")
		}
		fmt.Fprintf(&b, "## %s\n\n", s.Title)
		for _, it := range s.Items {
			switch it.Kind {
			case Prose:
				b.WriteString(demoteHeadings(it.Text) + "\n\n")
			case Quote:
				b.WriteString(quoteMarkdown(it))
			case Note:
				fmt.Fprintf(&b, "> %s\n\n", oneLine(it.Text))
			case Example:
				fmt.Fprintf(&b, "```%s\n%s\n```\n\n", it.Lang, strings.TrimRight(it.Text, "\n"))
			case Diagram:
				fmt.Fprintf(&b, "```mermaid\n%s\n```\n\n", it.Text)
				if it.Caption != "" {
					fmt.Fprintf(&b, "_%s_\n\n", it.Caption)
				}
			case Run:
				fmt.Fprintf(&b, "```console\n$ %s\n%s\n```\n\n", it.Run.Cmd, it.Text)
			case Shot:
				fmt.Fprintf(&b, "![%s](%s)\n\n", it.Caption, it.Shot.Src)
				for _, m := range it.Shot.Mark {
					switch {
					case m.Label == "":
					case m.N > 0:
						fmt.Fprintf(&b, "%d. %s\n", m.N, m.Label)
					default:
						fmt.Fprintf(&b, "- %s\n", m.Label)
					}
				}
				b.WriteString("\n")
			}
		}
	}
	return bytes.TrimRight(b.Bytes(), "\n")
}

func quoteMarkdown(it Item) string {
	q := it.Quote
	if q.Err != "" {
		return fmt.Sprintf("> _could not quote %s · %s: %s_\n\n", q.Repo, q.Path, q.Err)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "**%s · `%s` · %s**\n\n```%s\n", q.Repo, q.Path, LineLabel(q.Start, q.End), strings.ToLower(it.Lang))
	for _, l := range q.Lines {
		sb.WriteString(l + "\n")
	}
	sb.WriteString("```\n\n")
	for _, n := range append(slices.Clone(q.Notes), q.Loose...) {
		fmt.Fprintf(&sb, "> **line %s**: %s\n\n", LineLabel(n.Line, n.End), oneLine(n.Text))
	}
	return sb.String()
}

func demoteHeadings(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if m := heading.FindStringSubmatch(l); m != nil && len(m[1]) < 6 {
			lines[i] = "#" + l
		}
	}
	return strings.Join(lines, "\n")
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// HTML is the tour as one self-contained page: steps, code, styles and script, with nothing to fetch, so it can be
// published as an artifact or sent to someone with none of the repositories. One step is shown at a time, with a
// Present button for full screen.
func HTML(t Tour, o HTMLOptions) []byte {
	var b bytes.Buffer
	title := cmp.Or(t.Title, t.Name)
	b.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString(`<meta name="viewport" content="width=device-width, initial-scale=1">` + "\n")
	b.WriteString(`<meta name="color-scheme" content="dark light">` + "\n")
	fmt.Fprintf(&b, "<title>%s</title>\n<style>\n", html.EscapeString(title))
	b.WriteString(cmp.Or(o.Palette, DefaultPalette()))
	b.WriteString(pageCSS)
	b.WriteString("</style>\n</head>\n<body class=\"tour-export\">\n")
	fmt.Fprintf(&b, "<header class=\"tx-head\">\n<div class=\"tx-title\">%s</div>\n<div class=\"tx-meta\">", html.EscapeString(title))
	if t.Kind != "" {
		fmt.Fprintf(&b, `<span class="tx-kind">%s</span>`, html.EscapeString(t.Kind))
	}
	for _, r := range t.Repos {
		fmt.Fprintf(&b, `<span class="tx-repo">%s`, html.EscapeString(r.Name))
		if r.Base != "" && r.Commit != "" {
			fmt.Fprintf(&b, ` <span class="tx-sha">%s %s</span>`, html.EscapeString(r.Base), shortSHA(r.Commit))
		}
		b.WriteString(`</span>`)
	}
	b.WriteString("</div>\n</header>\n<main id=\"tx-steps\">\n")
	md := mdOpts{img: func(src, alt string) string {
		return fmt.Sprintf(`<img class="tour-img" src="%s" alt="%s">`, html.EscapeString(DataURI(t.Root, src)), html.EscapeString(alt))
	}}
	for i, s := range t.Steps {
		on := ""
		if i == 0 {
			on = " on"
		}
		fmt.Fprintf(&b, "<section class=\"tx-step%s\" data-i=\"%d\">\n<div class=\"tx-stepnum\">Step %d of %d</div>\n<h1 class=\"tx-steptitle\">%s</h1>\n",
			on, i, i+1, len(t.Steps), html.EscapeString(s.Title))
		for _, it := range s.Items {
			b.WriteString(itemHTML(t.Root, it, o, md))
		}
		b.WriteString("</section>\n")
	}
	b.WriteString("</main>\n")
	b.WriteString(`<footer class="tx-nav">
<button id="tx-prev" class="tx-btn" type="button">← Previous</button>
<div id="tx-dots" class="tx-dots"></div>
<button id="tx-next" class="tx-btn" type="button">Next →</button>
<button id="tx-present" class="tx-btn plain" type="button" title="Full screen, one step at a time (P)">Present</button>
</footer>
`)
	b.WriteString("<script>\n" + pageJS + "</script>\n</body>\n</html>\n")
	return b.Bytes()
}

func itemHTML(root string, it Item, o HTMLOptions, md mdOpts) string {
	switch it.Kind {
	case Prose:
		return `<div class="tour-prose">` + markdown(it.Text, md) + "</div>\n"
	case Note:
		return `<div class="tour-aside">` + markdown(it.Text, md) + "</div>\n"
	case Quote:
		return quoteHTML(it, o, md)
	case Example:
		lines := strings.Split(strings.TrimRight(it.Text, "\n"), "\n")
		var sb strings.Builder
		fmt.Fprintf(&sb, `<figure class="tour-example"><figcaption><span class="tq-tag">example</span><span class="tq-grow"></span><span class="tq-lang">%s</span></figcaption><div class="tq-code">`,
			html.EscapeString(it.Lang))
		spans := highlight(o, "", it.Lang, []byte(it.Text), 1, len(lines))
		for i, l := range lines {
			fmt.Fprintf(&sb, `<div class="tq-line"><code>%s</code></div>`, codeHTML(l, spans, i))
		}
		sb.WriteString("</div></figure>\n")
		return sb.String()
	case Diagram:
		capt := ""
		if it.Caption != "" {
			capt = `<figcaption class="td-cap">` + inline(it.Caption, md) + `</figcaption>`
		}
		return fmt.Sprintf(`<figure class="tour-diagram"><div class="tour-mermaid"><div class="td-kind">mermaid</div><pre class="mermaid">%s</pre></div>%s</figure>`+"\n",
			html.EscapeString(it.Text), capt)
	case Run:
		r := it.Run
		cwd, status, meta := "", "", ""
		if r.Cwd != "" {
			cwd = `<span class="tr-cwd">` + html.EscapeString(r.Cwd) + `</span>`
		}
		if r.Exit != 0 {
			status = fmt.Sprintf(`<span class="tr-exit">exit %d</span>`, r.Exit)
		}
		if !r.At.IsZero() {
			meta = "captured " + r.At.UTC().Format("2 Jan 15:04")
		}
		return fmt.Sprintf(`<figure class="tour-run"><figcaption>%s<span class="tr-prompt">$</span><span class="tr-cmd">%s</span><span class="tq-grow"></span>%s<span class="tr-meta">%s</span></figcaption><pre class="tr-out">%s</pre></figure>`+"\n",
			cwd, html.EscapeString(r.Cmd), status, meta, html.EscapeString(it.Text))
	case Shot:
		return shotHTML(root, it)
	}
	return ""
}

func highlight(o HTMLOptions, path, lang string, src []byte, start, end int) [][]Span {
	if o.Highlight == nil {
		return nil
	}
	return o.Highlight(path, lang, src, start, end)
}

// codeHTML is line i of a block: its spans when they cover it, else its text.
func codeHTML(line string, spans [][]Span, i int) string {
	if i >= len(spans) || spans[i] == nil {
		return html.EscapeString(line)
	}
	var sb strings.Builder
	for _, sp := range spans[i] {
		if sp.Class == "" {
			sb.WriteString(html.EscapeString(sp.Text))
			continue
		}
		fmt.Fprintf(&sb, `<span class="%s">%s</span>`, sp.Class, html.EscapeString(sp.Text))
	}
	return sb.String()
}

func quoteHTML(it Item, o HTMLOptions, md mdOpts) string {
	q := it.Quote
	var sb strings.Builder
	if q.Err != "" {
		fmt.Fprintf(&sb, `<figure class="tour-quote missing"><figcaption><span class="tq-repo">%s</span><span class="tq-sep">·</span><span class="tq-path">%s</span></figcaption><div class="tq-code"><div class="tq-missing">%s</div></div></figure>`+"\n",
			html.EscapeString(q.Repo), html.EscapeString(q.Path), html.EscapeString(q.Err))
		return sb.String()
	}
	rev := ""
	if q.Rev != "" {
		rev = `<span class="tq-rev" title="quoted at another revision">@ ` + html.EscapeString(q.Rev) + `</span>`
	}
	fmt.Fprintf(&sb, `<figure class="tour-quote"><figcaption><span class="tq-repo">%s</span><span class="tq-path">%s</span><span class="tq-grow"></span>%s<span class="tq-lines">L%s</span><span class="tq-lang">%s</span></figcaption>`,
		html.EscapeString(q.Repo), html.EscapeString(q.Path), rev, LineLabel(q.Start, q.End), html.EscapeString(it.Lang))
	collapsed := ""
	if q.Collapsed() {
		collapsed = " collapsed"
	}
	fmt.Fprintf(&sb, `<div class="tq-code%s">`, collapsed)
	spans := highlight(o, filepath.Join(q.Dir, q.Path), it.Lang, q.Src, q.Start, q.End)
	k := 0
	for n := q.Start; n <= q.End; n++ {
		cls := "tq-line"
		if q.Marked[n] {
			cls += " marked"
		}
		if !q.Visible(n) {
			cls += " tq-hidden"
		}
		fmt.Fprintf(&sb, `<div class="%s" data-n="%d"><span class="tq-n">%d</span><code>%s</code></div>`, cls, n, n, codeHTML(q.Lines[n-q.Start], spans, n-q.Start))
		for _, note := range q.NotesAt(n) {
			k++
			sb.WriteString(noteHTML(k, note, md))
		}
	}
	if h := q.Hidden(); h > 0 {
		fmt.Fprintf(&sb, `<button class="tq-more" type="button">… %d more line%s</button>`, h, plural(h))
	}
	if q.Clipped {
		sb.WriteString(`<div class="tq-clip">the range ran past the end of the file</div>`)
	}
	for _, note := range q.Loose {
		k++
		sb.WriteString(noteHTML(k, note, md))
	}
	sb.WriteString("</div></figure>\n")
	return sb.String()
}

func noteHTML(k int, n PinnedNote, md mdOpts) string {
	return fmt.Sprintf(`<div class="tq-note" data-line="%d"><span class="tn-n">%d</span><div class="tn-body">%s</div></div>`, n.Line, k, markdown(n.Text, md))
}

func shotHTML(root string, it Item) string {
	s := it.Shot
	if s.Src == "" {
		return `<figure class="tour-shot missing"><div class="ts-missing">a screenshot with no file named</div></figure>` + "\n"
	}
	src := DataURI(root, s.Src)
	if src == "" {
		return fmt.Sprintf(`<figure class="tour-shot missing"><div class="ts-missing">%s is not in this tour's assets</div></figure>`+"\n", html.EscapeString(s.Src))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, `<figure class="tour-shot"><div class="ts-wrap"><img src="%s" alt="%s">`, html.EscapeString(src), html.EscapeString(s.Alt))
	sb.WriteString(arrowSVG(s))
	for _, m := range s.Mark {
		sb.WriteString(markHTML(m))
	}
	sb.WriteString(`</div>`)
	if it.Caption != "" {
		fmt.Fprintf(&sb, `<figcaption class="ts-cap">%s</figcaption>`, inline(it.Caption, mdOpts{}))
	}
	sb.WriteString("</figure>\n")
	return sb.String()
}

// ArrowHead is an arrow's line end and its head's three points, in the image's own pixels so the head keeps its
// shape however the picture is scaled. It stops short of the point so the head sits beside what it points at.
func ArrowHead(m Mark, w, h float64) (x1, y1, bx, by float64, head [3][2]float64, ok bool) {
	x1, y1 = m.X/100*w, m.Y/100*h
	x2, y2 := m.X2/100*w, m.Y2/100*h
	dist := math.Hypot(x2-x1, y2-y1)
	if dist == 0 {
		return 0, 0, 0, 0, head, false
	}
	ux, uy := (x2-x1)/dist, (y2-y1)/dist
	size := math.Max(w*0.018, 10)
	tx, ty := x2-ux*size*.25, y2-uy*size*.25
	bx, by = tx-ux*size, ty-uy*size
	px, py := -uy*size*.42, ux*size*.42
	return x1, y1, bx, by, [3][2]float64{{tx, ty}, {bx + px, by + py}, {bx - px, by - py}}, true
}

func arrowSVG(s *ShotInfo) string {
	var lines strings.Builder
	w, h := float64(s.W), float64(s.H)
	for _, m := range s.Mark {
		if m.Kind != Arrow {
			continue
		}
		x1, y1, bx, by, hd, ok := ArrowHead(m, w, h)
		if !ok {
			continue
		}
		fmt.Fprintf(&lines, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f"/>`, x1, y1, bx, by)
		fmt.Fprintf(&lines, `<polygon points="%.1f,%.1f %.1f,%.1f %.1f,%.1f"/>`, hd[0][0], hd[0][1], hd[1][0], hd[1][1], hd[2][0], hd[2][1])
	}
	if lines.Len() == 0 {
		return ""
	}
	return fmt.Sprintf(`<svg class="ts-arrows" viewBox="0 0 %d %d" preserveAspectRatio="none" aria-hidden="true" style="stroke-width:%.1f">%s</svg>`,
		s.W, s.H, math.Max(w*0.004, 2), lines.String())
}

func markHTML(m Mark) string {
	num, label, cls := "", "", ""
	if m.N > 0 {
		num = fmt.Sprintf(`<span class="ts-n">%d</span>`, m.N)
	}
	if m.Label != "" {
		label = fmt.Sprintf(`<span class="ts-label">%s</span>`, html.EscapeString(m.Label))
	}
	if m.Kind == Box {
		if !m.LabelBelow {
			cls = " above"
		}
		return fmt.Sprintf(`<div class="ts-box%s" style="left:%.2f%%;top:%.2f%%;width:%.2f%%;height:%.2f%%">%s</div>`, cls, m.X, m.Y, m.X2, m.Y2, label)
	}
	if m.LabelBelow {
		cls += " below"
	}
	if m.LabelToLeft {
		cls += " left"
	}
	if m.AtLeftEdge {
		cls += " edge"
	}
	return fmt.Sprintf(`<div class="ts-mark %s%s" style="left:%.2f%%;top:%.2f%%">%s%s</div>`, m.Kind, cls, m.X, m.Y, num, label)
}

// DataURI inlines an image a step names; one that can't be read (or is over 8 MB) is "".
func DataURI(root, src string) string {
	if strings.HasPrefix(src, "data:") {
		return src
	}
	p, err := AssetPath(root, src)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) > 8<<20 {
		return ""
	}
	ct := cmp.Or(mime.TypeByExtension(strings.ToLower(filepath.Ext(p))), "application/octet-stream")
	return "data:" + ct + ";base64," + base64.StdEncoding.EncodeToString(b)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
