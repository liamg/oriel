// Package help is Oriel's help pages (help.md): markdown with front matter, indexed by page id and heading anchor,
// and searched for :help and the ␣h picker. Pages come from every contributor's fs.FS; Oriel's own are embedded.
package help

import (
	"bufio"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

//go:embed pages
var builtin embed.FS

// Builtin is Oriel's own pages.
func Builtin() fs.FS {
	sub, _ := fs.Sub(builtin, "pages")
	return sub
}

// Block is a part of a page: a heading, paragraph, list, code block, or a generated section ({{keys "glob"}}).
type Block struct {
	Kind   string // "heading", "para", "list", "code", "keys", "options", "live", "api"
	Level  int    // a heading's
	Anchor string // a heading's
	Text   string // a heading's, a paragraph's, a code block's, a directive's argument
	Items  []string
}

// Page is one help page.
type Page struct {
	ID      string
	Title   string
	Covers  []string // element id globs it explains
	Related []string // page ids, with an optional #anchor
	Source  string   // who shipped it: "builtin", a plugin's id
	File    string   // where it came from, within its source
	Blocks  []Block
}

// Anchors are the page's headings' anchors.
func (p *Page) Anchors() []string {
	var out []string
	for _, b := range p.Blocks {
		if b.Kind == "heading" {
			out = append(out, b.Anchor)
		}
	}
	return out
}

// Section is the blocks under the heading with anchor, down to the next heading as high; the whole page for "".
func (p *Page) Section(anchor string) (Block, []Block, bool) {
	if anchor == "" {
		return Block{Kind: "heading", Level: 1, Text: p.Title}, p.Blocks, true
	}
	for i, b := range p.Blocks {
		if b.Kind != "heading" || b.Anchor != anchor {
			continue
		}
		end := len(p.Blocks)
		for j := i + 1; j < len(p.Blocks); j++ {
			if p.Blocks[j].Kind == "heading" && p.Blocks[j].Level <= b.Level {
				end = j
				break
			}
		}
		return b, p.Blocks[i+1 : end], true
	}
	return Block{}, nil, false
}

var (
	headingRe   = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*(?:\{#([a-z0-9-]+)\})?$`)
	directiveRe = regexp.MustCompile(`^\{\{(keys|options|live|api)\s+"([^"]*)"\}\}$`)
	slugRe      = regexp.MustCompile(`[^a-z0-9]+`)
)

// Slug is a heading's anchor when it doesn't give one: lowercase words joined by hyphens.
func Slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// Parse reads a page: front matter between --- lines (id, covers, related), then markdown.
func Parse(src, source, file string) (*Page, error) {
	p := &Page{Source: source, File: file}
	sc := bufio.NewScanner(strings.NewReader(src))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	i := 0
	if len(lines) > 0 && lines[0] == "---" {
		for i = 1; i < len(lines) && lines[i] != "---"; i++ {
			k, v, _ := strings.Cut(lines[i], ":")
			v = strings.TrimSpace(v)
			switch strings.TrimSpace(k) {
			case "id":
				p.ID = v
			case "covers":
				p.Covers = list(v)
			case "related":
				p.Related = list(v)
			}
		}
		i++
	}
	if p.ID == "" {
		return nil, fmt.Errorf("%s: no id in its front matter", file)
	}
	var para []string
	flush := func() {
		if len(para) > 0 {
			p.Blocks = append(p.Blocks, Block{Kind: "para", Text: strings.Join(para, " ")})
			para = nil
		}
	}
	for ; i < len(lines); i++ {
		l := lines[i]
		t := strings.TrimSpace(l)
		switch {
		case t == "" || strings.HasPrefix(t, "<!--"):
			flush()
		case strings.HasPrefix(t, "```"):
			flush()
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			p.Blocks = append(p.Blocks, Block{Kind: "code", Text: strings.Join(code, "\n")})
		case headingRe.MatchString(t) && strings.HasPrefix(t, "#"):
			flush()
			m := headingRe.FindStringSubmatch(t)
			anchor := m[3]
			if anchor == "" {
				anchor = Slug(m[2])
			}
			if len(m[1]) == 1 && p.Title == "" {
				p.Title = m[2]
				continue
			}
			p.Blocks = append(p.Blocks, Block{Kind: "heading", Level: len(m[1]), Text: m[2], Anchor: anchor})
		case directiveRe.MatchString(t):
			flush()
			m := directiveRe.FindStringSubmatch(t)
			p.Blocks = append(p.Blocks, Block{Kind: m[1], Text: m[2]})
		case strings.HasPrefix(t, "- "):
			flush()
			if n := len(p.Blocks); n > 0 && p.Blocks[n-1].Kind == "list" {
				p.Blocks[n-1].Items = append(p.Blocks[n-1].Items, t[2:])
			} else {
				p.Blocks = append(p.Blocks, Block{Kind: "list", Items: []string{t[2:]}})
			}
		case strings.HasPrefix(l, "  ") && len(p.Blocks) > 0 && p.Blocks[len(p.Blocks)-1].Kind == "list" && len(para) == 0:
			items := p.Blocks[len(p.Blocks)-1].Items
			items[len(items)-1] += " " + t
		default:
			para = append(para, t)
		}
	}
	flush()
	if p.Title == "" {
		p.Title = p.ID
	}
	return p, nil
}

func list(v string) []string {
	v = strings.Trim(v, "[] ")
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Index is every page, by id; a later source's page of the same id wins, so config can override Oriel's.
type Index struct {
	pages map[string]*Page
	ids   []string
}

// Load reads the .md files of each source into an index. A page that won't parse is skipped and returned as an error.
func Load(sources []Source) (*Index, []error) {
	ix := &Index{pages: map[string]*Page{}}
	var errs []error
	for _, s := range sources {
		fs.WalkDir(s.FS, ".", func(name string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || path.Ext(name) != ".md" {
				return nil
			}
			b, err := fs.ReadFile(s.FS, name)
			if err != nil {
				errs = append(errs, err)
				return nil
			}
			p, err := Parse(string(b), s.Name, name)
			if err != nil {
				errs = append(errs, err)
				return nil
			}
			ix.pages[p.ID] = p
			return nil
		})
	}
	for id := range ix.pages {
		ix.ids = append(ix.ids, id)
	}
	slices.Sort(ix.ids)
	return ix, errs
}

// Source is a named fs of pages.
type Source struct {
	Name string
	FS   fs.FS
}

// Pages is every page, by id.
func (ix *Index) Pages() []*Page {
	out := make([]*Page, len(ix.ids))
	for i, id := range ix.ids {
		out[i] = ix.pages[id]
	}
	return out
}

// For finds a help id, "page" or "page#anchor": the page and whether the anchor is on it.
func (ix *Index) For(helpID string) (*Page, string, bool) {
	id, anchor, _ := strings.Cut(helpID, "#")
	p, ok := ix.pages[id]
	if !ok {
		return nil, "", false
	}
	if anchor != "" && !slices.Contains(p.Anchors(), anchor) {
		return p, anchor, false
	}
	return p, anchor, true
}

// Hit is a page section a search found.
type Hit struct {
	ID    string // the help id: "page#anchor"
	Title string
	Page  *Page
}

// Topics are every page and section, as help ids with their titles, for :help and the ␣h picker.
func (ix *Index) Topics() []Hit {
	var out []Hit
	for _, p := range ix.Pages() {
		out = append(out, Hit{ID: p.ID, Title: p.Title, Page: p})
		for _, b := range p.Blocks {
			if b.Kind == "heading" {
				out = append(out, Hit{ID: p.ID + "#" + b.Anchor, Title: b.Text, Page: p})
			}
		}
	}
	return out
}
