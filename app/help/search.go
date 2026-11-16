package help

import (
	"slices"
	"strings"
	"unicode"
)

// Result is a section a search found, best first: its help id, its title, a line of it around what matched, and
// how well it matched.
type Result struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Snippet string `json:"snippet,omitempty"`
	Score   int    `json:"-"`
}

// Expand is a directive's block as text ({{keys}}, {{options}}…), as it reads now; "" leaves it out.
type Expand func(Block) string

// stopWords are left out of a query: "how do I split the editor" searches for split and editor.
var stopWords = map[string]bool{"a": true, "an": true, "and": true, "the": true, "to": true, "of": true, "in": true, "on": true,
	"is": true, "it": true, "i": true, "do": true, "does": true, "how": true, "can": true, "what": true, "where": true,
	"my": true, "ve": true, "ll": true, "re": true, "s": true, "t": true, "d": true, "m": true, "me": true, "for": true, "with": true, "oriel": true, "you": true, "are": true, "there": true, "way": true}

func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// Search finds the n sections that best match query's words: a word in a section's heading counts most, then each
// time it's in its text (with its directives expanded), and a section with every word beats one with some.
func (ix *Index) Search(query string, n int, expand Expand) []Result {
	var terms []string
	for _, w := range words(query) {
		if !stopWords[w] && !slices.Contains(terms, w) {
			terms = append(terms, w)
		}
	}
	if len(terms) == 0 {
		return nil
	}
	var out []Result
	for _, sec := range ix.sections() {
		title := strings.ToLower(sec.title + " " + sec.page.Title)
		text := strings.ToLower(BlocksText(sec.blocks, expand))
		score, found := 0, 0
		for _, t := range terms {
			s := 0
			if slices.ContainsFunc(words(title), func(w string) bool { return strings.HasPrefix(w, t) }) {
				s += 4
			}
			s += min(3, strings.Count(text, t))
			if s > 0 {
				found++
			}
			score += s
		}
		if found == 0 {
			continue
		}
		score += 3 * found * found
		out = append(out, Result{ID: sec.id, Title: sec.title, Snippet: snippet(BlocksText(sec.blocks, expand), terms), Score: score})
	}
	slices.SortStableFunc(out, func(a, b Result) int { return b.Score - a.Score })
	return out[:min(n, len(out))]
}

type section struct {
	id, title string
	page      *Page
	blocks    []Block
}

// sections are each page's opening (what's before its first heading) and each heading's own blocks, down to the
// next heading.
func (ix *Index) sections() []section {
	var out []section
	for _, p := range ix.Pages() {
		cur := section{id: p.ID, title: p.Title, page: p}
		for _, b := range p.Blocks {
			if b.Kind == "heading" {
				out = append(out, cur)
				cur = section{id: p.ID + "#" + b.Anchor, title: b.Text, page: p}
				continue
			}
			cur.blocks = append(cur.blocks, b)
		}
		out = append(out, cur)
	}
	return out
}

// BlocksText is blocks as markdown: headings, paragraphs, lists and code as written, directives as expand gives them.
func BlocksText(blocks []Block, expand Expand) string {
	var sb strings.Builder
	for _, b := range blocks {
		var s string
		switch b.Kind {
		case "heading":
			s = strings.Repeat("#", b.Level) + " " + b.Text
		case "para":
			s = b.Text
		case "list":
			s = "- " + strings.Join(b.Items, "\n- ")
		case "code":
			s = "```\n" + b.Text + "\n```"
		default:
			if expand != nil {
				s = expand(b)
			}
		}
		if s = strings.TrimSpace(s); s != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(s)
		}
	}
	return sb.String()
}

// SectionText is a help id's section as markdown, with its title, and whether there is one.
func (ix *Index) SectionText(id string, expand Expand) (title, text string, ok bool) {
	p, anchor, ok := ix.For(id)
	if !ok {
		return "", "", false
	}
	head, blocks, _ := p.Section(anchor)
	return head.Text, BlocksText(blocks, expand), true
}

const snippetLen = 160

// snippet is the line of text with the most of terms in it (the first of those), cut to about snippetLen around the
// first of them.
func snippet(text string, terms []string) string {
	line, best := "", 0
	for _, l := range strings.Split(text, "\n") {
		n := 0
		for _, t := range terms {
			if strings.Contains(strings.ToLower(l), t) {
				n++
			}
		}
		if n > best {
			line, best = l, n
		}
	}
	lower := strings.ToLower(line)
	at := len(line)
	for _, t := range terms {
		if i := strings.Index(lower, t); i >= 0 {
			at = min(at, i)
		}
	}
	if r := []rune(line); len(r) > snippetLen {
		off := len([]rune(line[:at]))
		from := max(0, min(off-snippetLen/3, len(r)-snippetLen))
		line = string(r[from : from+snippetLen])
		if from > 0 {
			line = "…" + line
		}
		if from+snippetLen < len(r) {
			line += "…"
		}
	}
	return strings.TrimSpace(line)
}
