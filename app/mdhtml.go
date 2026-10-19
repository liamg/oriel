package app

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// mdImage is an image in markdown: ![alt](src) or <img>, with the size its attributes ask for.
type mdImage struct {
	Src, Alt string
	W, H     float32 // in pixels; 0 when not given
	WPct     float32 // a width given as a share of the column, 0..1
}

// htmlTok is a piece of HTML: text, a start or end tag with its attributes, or a comment.
type htmlTok struct {
	kind  byte // 't' text, '<' start, '/' end, '!' comment
	name  string
	attrs map[string]string
	text  string
}

// htmlTokens splits s into text, tags and comments. A '<' that starts no tag is text.
func htmlTokens(s string) []htmlTok {
	var out []htmlTok
	text := func(t string) {
		if t == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].kind == 't' {
			out[n-1].text += t
			return
		}
		out = append(out, htmlTok{kind: 't', text: t})
	}
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			text(s)
			break
		}
		text(s[:i])
		s = s[i:]
		if strings.HasPrefix(s, "<!--") {
			end := strings.Index(s, "-->")
			if end < 0 {
				end = len(s) - 3
			}
			out = append(out, htmlTok{kind: '!', text: s[4:max(end, 4)]})
			s = s[end+3:]
			continue
		}
		t, n := htmlTag(s)
		if n == 0 {
			text("<")
			s = s[1:]
			continue
		}
		out = append(out, t)
		s = s[n:]
	}
	return out
}

// htmlTag reads the tag s starts with, and how long it is; 0 when s doesn't start with one.
func htmlTag(s string) (htmlTok, int) {
	t := htmlTok{kind: '<'}
	i := 1
	if i < len(s) && s[i] == '/' {
		t.kind = '/'
		i++
	}
	start := i
	for i < len(s) && (isAlpha(s[i]) || i > start && (s[i] >= '0' && s[i] <= '9' || s[i] == '-')) {
		i++
	}
	if i == start {
		return t, 0
	}
	t.name = strings.ToLower(s[start:i])
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '/') {
			i++
		}
		if i >= len(s) {
			return t, 0
		}
		if s[i] == '>' {
			return t, i + 1
		}
		k := i
		for i < len(s) && !strings.ContainsRune(" \t\n\r=>/", rune(s[i])) {
			i++
		}
		name, val := strings.ToLower(s[k:i]), ""
		if i < len(s) && s[i] == '=' {
			i++
			if i < len(s) && (s[i] == '"' || s[i] == '\'') {
				q := s[i]
				e := strings.IndexByte(s[i+1:], q)
				if e < 0 {
					return t, 0
				}
				val = s[i+1 : i+1+e]
				i += e + 2
			} else {
				k := i
				for i < len(s) && !strings.ContainsRune(" \t\n\r>", rune(s[i])) {
					i++
				}
				val = s[k:i]
			}
		}
		if t.attrs == nil {
			t.attrs = map[string]string{}
		}
		t.attrs[name] = html.UnescapeString(val)
	}
	return t, 0
}

func isAlpha(b byte) bool { return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' }

// htmlBlockTags are the tags that start an HTML block in markdown, as GitHub renders a README's.
var htmlBlockTags = map[string]bool{"p": true, "div": true, "center": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true,
	"h6": true, "details": true, "summary": true, "picture": true, "table": true, "ul": true, "ol": true, "li": true, "hr": true, "br": true,
	"section": true, "figure": true, "blockquote": true, "dl": true, "pre": true}

var htmlStart = regexp.MustCompile(`^\s{0,3}<(!--|/?([A-Za-z][A-Za-z0-9]*))`)

// startsHTMLBlock reports whether a line starts an HTML block: a block tag or a comment.
func startsHTMLBlock(line string) bool {
	m := htmlStart.FindStringSubmatch(line)
	return m != nil && (m[1] == "!--" || htmlBlockTags[strings.ToLower(m[2])])
}

// htmlBlocks turns an HTML block, lines [line, end] of the source, into markdown blocks: headings, paragraphs with
// their images and alignment, list items and rules. Inline tags become the inline markdown they mean (<b> **, <code>
// and <kbd> `); comments and tags it doesn't know are dropped.
func htmlBlocks(src string, line, end int) []mdBlock {
	var out []mdBlock
	cur := mdBlock{Kind: mdPara, Line: line, End: end}
	var text strings.Builder
	var aligns []string
	align := func() string {
		for i := len(aligns) - 1; i >= 0; i-- {
			if aligns[i] != "" {
				return aligns[i]
			}
		}
		return ""
	}
	flush := func() {
		t := strings.Join(strings.Fields(text.String()), " ")
		t = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(t, "** **", " "), "****", ""))
		if t != "" && t != "**" || len(cur.Images) > 0 {
			cur.Text, cur.Align = t, align()
			out = append(out, cur)
		}
		cur = mdBlock{Kind: mdPara, Line: line, End: end}
		text.Reset()
	}
	for _, tk := range htmlTokens(src) {
		switch tk.kind {
		case 't':
			text.WriteString(html.UnescapeString(tk.text))
		case '<':
			switch tk.name {
			case "h1", "h2", "h3", "h4", "h5", "h6":
				flush()
				aligns = append(aligns, tk.attrs["align"])
				cur.Kind, cur.Level = mdHeading, int(tk.name[1]-'0')
			case "p", "div", "section", "figure", "blockquote", "table", "tr", "details", "dl", "pre", "ul", "ol":
				flush()
				aligns = append(aligns, tk.attrs["align"])
			case "center":
				flush()
				aligns = append(aligns, "center")
			case "summary":
				flush()
				text.WriteString("**▸ ")
			case "li":
				flush()
				cur.Kind, cur.Mark = mdItem, "•"
			case "br":
				flush()
			case "hr":
				flush()
				out = append(out, mdBlock{Kind: mdRule, Line: line, End: end})
			case "img":
				cur.Images = append(cur.Images, imgAttrs(tk.attrs))
			case "b", "strong":
				text.WriteString(" **")
			case "code", "kbd", "tt", "samp":
				text.WriteString("`")
			case "td", "th":
				text.WriteString(" ")
			}
		case '/':
			switch tk.name {
			case "h1", "h2", "h3", "h4", "h5", "h6", "p", "div", "section", "figure", "blockquote", "table", "tr", "details", "dl", "pre",
				"ul", "ol", "center":
				flush()
				if len(aligns) > 0 {
					aligns = aligns[:len(aligns)-1]
				}
			case "summary":
				text.WriteString("**")
				flush()
			case "li":
				flush()
			case "b", "strong":
				text.WriteString("** ")
			case "code", "kbd", "tt", "samp":
				text.WriteString("`")
			}
		}
	}
	flush()
	return out
}

// imgAttrs is the image an <img> tag's attributes describe.
func imgAttrs(a map[string]string) mdImage {
	im := mdImage{Src: a["src"], Alt: a["alt"]}
	if w := strings.TrimSpace(a["width"]); strings.HasSuffix(w, "%") {
		if f, err := strconv.ParseFloat(strings.TrimSuffix(w, "%"), 32); err == nil {
			im.WPct = float32(f) / 100
		}
	} else {
		im.W = pixels(w)
	}
	im.H = pixels(a["height"])
	return im
}

func pixels(s string) float32 {
	f, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "px"), 32)
	if err != nil || f <= 0 {
		return 0
	}
	return float32(f)
}

var (
	mdImageRe   = regexp.MustCompile(`!\[([^\]]*)\]\(\s*<?([^)\s>]+)>?(?:\s+"[^"]*")?\s*\)`)
	mdLinkedImg = regexp.MustCompile(`\[\s*(!\[[^\]]*\]\([^)]*\))\s*\]\([^)]*\)`)
	htmlImgRe   = regexp.MustCompile(`(?is)<img\b[^>]*>`)
)

// mdTakeImages takes the images out of a paragraph's inline markdown: ![alt](src), one inside a link, and <img>. It
// returns the text without them.
func mdTakeImages(s string) (string, []mdImage) {
	if !strings.Contains(s, "![") && !strings.Contains(strings.ToLower(s), "<img") {
		return s, nil
	}
	type found struct {
		at int
		im mdImage
	}
	var all []found
	s = mdLinkedImg.ReplaceAllString(s, "$1")
	for _, m := range mdImageRe.FindAllStringSubmatchIndex(s, -1) {
		all = append(all, found{m[0], mdImage{Alt: s[m[2]:m[3]], Src: s[m[4]:m[5]]}})
	}
	for _, m := range htmlImgRe.FindAllStringIndex(s, -1) {
		if t, n := htmlTag(s[m[0]:m[1]]); n > 0 {
			all = append(all, found{m[0], imgAttrs(t.attrs)})
		}
	}
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && all[j].at < all[j-1].at; j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	out := make([]mdImage, len(all))
	for i, f := range all {
		out[i] = f.im
	}
	s = htmlImgRe.ReplaceAllString(mdImageRe.ReplaceAllString(s, ""), "")
	return strings.TrimSpace(s), out
}

// mdInlineHTML is inline markdown with its HTML made markdown: <b> and <strong> bold, <code> and <kbd> code, <br> a
// space, entities decoded, comments and other tags dropped.
func mdInlineHTML(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	var b strings.Builder
	for i, part := range strings.Split(s, "`") {
		if i > 0 {
			b.WriteByte('`')
		}
		if i%2 == 1 {
			b.WriteString(part) // inside a code span
			continue
		}
		for _, tk := range htmlTokens(part) {
			switch tk.kind {
			case 't':
				b.WriteString(html.UnescapeString(tk.text))
			case '<', '/':
				switch tk.name {
				case "b", "strong":
					b.WriteString("**")
				case "code", "kbd", "tt", "samp":
					b.WriteString("`")
				case "br":
					b.WriteString(" ")
				}
			}
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
