package tour

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// Markdown for the HTML export's prose. Everything is escaped first: nothing in a tour needs raw HTML, so there's no
// tag to smuggle through.

var (
	reULItem  = regexp.MustCompile(`^\s*[-*+]\s+(.*)$`)
	reOLItem  = regexp.MustCompile(`^\s*(\d+)[.)]\s+(.*)$`)
	reQuote   = regexp.MustCompile(`^\s*>\s?(.*)$`)
	reHR      = regexp.MustCompile(`^\s*(-{3,}|\*{3,}|_{3,})\s*$`)
	reTableIn = regexp.MustCompile(`^[|\s:-]+$`)

	reCodeSpan = regexp.MustCompile("`([^`\n]+)`")
	reImage    = regexp.MustCompile(`!\[([^\]\n]*)\]\(([^)\s]+)\)`)
	reLink     = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\s]+)\)`)
	reBold     = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	reItalic   = regexp.MustCompile(`(^|[\s(])_([^_\n]+)_([\s.,;:)!?]|$)`)
	reItalicA  = regexp.MustCompile(`(^|[\s(])\*([^*\n]+)\*([\s.,;:)!?]|$)`)
	reStrike   = regexp.MustCompile(`~~([^~\n]+)~~`)
	reBareURL  = regexp.MustCompile(`(^|[\s(])(https?://[^\s<>()"']+)`)
)

// mdOpts is how an image path becomes a src: the export inlines them as data URIs.
type mdOpts struct {
	img func(src, alt string) string
}

// markdown renders prose to HTML.
func markdown(src string, o mdOpts) string {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var out strings.Builder
	var para []string
	var item []string // the list item being built, so a wrapped one stays one
	list := ""        // "ul" | "ol"

	// Hard wraps join into a paragraph; a deliberate break is two trailing spaces.
	flushPara := func() {
		if len(para) == 0 {
			return
		}
		fmt.Fprintf(&out, "<p>%s</p>\n", strings.Join(para, "\n"))
		para = nil
	}
	flushItem := func() {
		if len(item) == 0 {
			return
		}
		fmt.Fprintf(&out, "<li>%s</li>\n", strings.Join(item, "\n"))
		item = nil
	}
	flushList := func() {
		flushItem()
		if list != "" {
			fmt.Fprintf(&out, "</%s>\n", list)
			list = ""
		}
	}

	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		// A blank line ends the item, not the list: prose that follows closes it below.
		if strings.TrimSpace(line) == "" {
			flushPara()
			flushItem()
			continue
		}

		// A table: a row with pipes, then a separator row.
		if strings.Contains(line, "|") && i+1 < len(lines) &&
			strings.Contains(lines[i+1], "-") && strings.Contains(lines[i+1], "|") &&
			reTableIn.MatchString(strings.TrimSpace(lines[i+1])) {
			flushPara()
			flushList()
			head := tableCells(line)
			i++ // the separator
			var rows [][]string
			for i+1 < len(lines) && strings.Contains(lines[i+1], "|") && strings.TrimSpace(lines[i+1]) != "" {
				i++
				rows = append(rows, tableCells(lines[i]))
			}
			out.WriteString(`<div class="tblwrap"><table><thead><tr>`)
			for _, h := range head {
				fmt.Fprintf(&out, "<th>%s</th>", inline(h, o))
			}
			out.WriteString("</tr></thead><tbody>")
			for _, r := range rows {
				out.WriteString("<tr>")
				for _, c := range r {
					fmt.Fprintf(&out, "<td>%s</td>", inline(c, o))
				}
				out.WriteString("</tr>")
			}
			out.WriteString("</tbody></table></div>\n")
			continue
		}

		if m := heading.FindStringSubmatch(line); m != nil {
			flushPara()
			flushList()
			lvl := min(len(m[1])+1, 6)
			fmt.Fprintf(&out, "<h%d>%s</h%d>\n", lvl, inline(m[2], o), lvl)
			continue
		}
		if reHR.MatchString(line) {
			flushPara()
			flushList()
			out.WriteString("<hr>\n")
			continue
		}
		if m := reQuote.FindStringSubmatch(line); m != nil {
			flushPara()
			flushList()
			fmt.Fprintf(&out, "<blockquote>%s</blockquote>\n", inline(m[1], o))
			continue
		}
		if m := reULItem.FindStringSubmatch(line); m != nil {
			flushPara()
			flushItem()
			if list != "ul" {
				flushList()
				out.WriteString("<ul>\n")
				list = "ul"
			}
			item = append(item, inline(m[1], o))
			continue
		}
		if m := reOLItem.FindStringSubmatch(line); m != nil {
			flushPara()
			flushItem()
			if list != "ol" {
				flushList()
				// The numbering comes from the source, so prose between items doesn't restart the count.
				if m[1] == "1" {
					out.WriteString("<ol>\n")
				} else {
					fmt.Fprintf(&out, "<ol start=%q>\n", m[1])
				}
				list = "ol"
			}
			item = append(item, inline(m[2], o))
			continue
		}
		text := inline(strings.TrimSpace(line), o)
		if strings.HasSuffix(lines[i], "  ") {
			text += "<br>"
		}
		// A wrapped bullet is still one bullet.
		if list != "" && len(item) > 0 {
			item = append(item, text)
			continue
		}
		flushList()
		para = append(para, text)
	}
	flushPara()
	flushList()
	return out.String()
}

func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// inline renders one line's spans. Code spans are lifted out first so nothing in them is read as markup.
func inline(s string, o mdOpts) string {
	var codes []string
	s = reCodeSpan.ReplaceAllStringFunc(s, func(m string) string {
		codes = append(codes, "<code>"+html.EscapeString(strings.Trim(m, "`"))+"</code>")
		return fmt.Sprintf("\x00C%d\x00", len(codes)-1)
	})
	s = html.EscapeString(s)

	s = reImage.ReplaceAllStringFunc(s, func(m string) string {
		p := reImage.FindStringSubmatch(m)
		alt, src := p[1], p[2]
		if o.img != nil {
			return o.img(src, alt)
		}
		return fmt.Sprintf(`<img src="%s" alt="%s">`, html.EscapeString(src), html.EscapeString(alt))
	})
	s = reLink.ReplaceAllStringFunc(s, func(m string) string {
		p := reLink.FindStringSubmatch(m)
		text, href := p[1], p[2]
		if !safeHref(href) {
			return text
		}
		target := ""
		if strings.HasPrefix(href, "http") {
			target = ` target="_blank" rel="noopener"`
		}
		return fmt.Sprintf(`<a href="%s"%s>%s</a>`, href, target, text)
	})
	// Bold first, so **this** is never read as an emphasised *this*.
	s = reBold.ReplaceAllString(s, "<strong>$1</strong>")
	s = reItalic.ReplaceAllString(s, "$1<em>$2</em>$3")
	s = reItalicA.ReplaceAllString(s, "$1<em>$2</em>$3")
	s = reStrike.ReplaceAllString(s, "<del>$1</del>")
	s = reBareURL.ReplaceAllString(s, `$1<a href="$2" target="_blank" rel="noopener">$2</a>`)

	for i, c := range codes {
		s = strings.ReplaceAll(s, fmt.Sprintf("\x00C%d\x00", i), c)
	}
	return s
}

// safeHref keeps javascript: and friends out; relative links stay.
func safeHref(href string) bool {
	l := strings.ToLower(strings.TrimSpace(href))
	if strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "#") {
		return true
	}
	return !strings.Contains(l, ":")
}
