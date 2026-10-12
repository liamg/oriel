// Package refcheck verifies that test references resolve in the vendored specs under spec/,
// and that their quotes appear there verbatim.
//
// A reference is a "ref:" line, optionally followed by a "quote:" line, in a .vt file or a Go test comment:
//
//	ref:    motion.txt *w*              vim help tag (spec/vim)
//	ref:    nvim/motion.txt *w*         nvim help tag (spec/nvim)
//	ref:    lsp/specification.md#Text Documents   markdown heading
//	ref:    css-color-4/Overview.bs#hue-interpolation   HTML or Bikeshed section, by its heading's id
//	ref:    css-color-4/conversions.js#OKLab_to_XYZ     a function in vendored sample code
//	ref:    oracle                      expectation taken from the nvim oracle; no quote
//	quote:  "[count] words forward."
package refcheck

import (
	"bufio"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Ref is one reference found in a test file.
type Ref struct {
	File  string // where the reference was written
	Line  int
	Ref   string
	Quote string
}

func (r Ref) String() string { return fmt.Sprintf("%s:%d", r.File, r.Line) }

// helpSection is how many lines after a tag its section may run.
const helpSection = 100

var (
	refLine   = regexp.MustCompile(`^\s*(?://\s*)?ref:\s*(.+?)\s*$`)
	quoteLine = regexp.MustCompile(`^\s*(?://\s*)?quote:\s*(.+?)\s*$`)
	helpTag   = regexp.MustCompile(`\*([^*\s|]+)\*`)
)

// Scan reads the references in one file.
func Scan(path string) ([]Ref, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var refs []Ref
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if m := refLine.FindStringSubmatch(line); m != nil {
			refs = append(refs, Ref{File: path, Line: n, Ref: m[1]})
		} else if m := quoteLine.FindStringSubmatch(line); m != nil {
			if len(refs) == 0 || refs[len(refs)-1].Quote != "" {
				return nil, fmt.Errorf("%s:%d: quote without a ref", path, n)
			}
			q, err := unquote(m[1])
			if err != nil {
				return nil, fmt.Errorf("%s:%d: %v", path, n, err)
			}
			refs[len(refs)-1].Quote = q
		}
	}
	return refs, sc.Err()
}

func unquote(s string) (string, error) {
	if !strings.HasPrefix(s, `"`) {
		return s, nil
	}
	q, err := strconv.Unquote(s)
	if err != nil {
		return "", fmt.Errorf("bad quote %s: %v", s, err)
	}
	return q, nil
}

// Checker resolves references against spec/ under Root, caching what it reads.
type Checker struct {
	Root  string
	files map[string][]string
}

// Check returns nil if r resolves and its quote appears in the referenced section.
func (c *Checker) Check(r Ref) error {
	ref := r.Ref
	if ref == "oracle" || strings.HasPrefix(ref, "oracle ") {
		return nil
	}
	if file, heading, ok := strings.Cut(ref, "#"); ok {
		switch filepath.Ext(file) {
		case ".md":
			return c.checkMarkdown(r, file, heading)
		case ".bs", ".html":
			return c.checkHTML(r, file, heading)
		case ".js":
			return c.checkFunc(r, file, heading)
		}
	}
	file, anchor, ok := strings.Cut(ref, " ")
	anchor = strings.TrimSpace(anchor)
	if ok && strings.HasSuffix(file, ".txt") && strings.HasPrefix(anchor, "*") && strings.HasSuffix(anchor, "*") {
		if !strings.Contains(file, "/") {
			file = "vim/" + file
		}
		return c.checkHelp(r, file, strings.Trim(anchor, "*"))
	}
	return fmt.Errorf("%s: unrecognised ref %q", r, ref)
}

func (c *Checker) lines(file string) ([]string, error) {
	if l, ok := c.files[file]; ok {
		return l, nil
	}
	b, err := os.ReadFile(filepath.Join(c.Root, "spec", filepath.FromSlash(file)))
	if err != nil {
		return nil, err
	}
	if c.files == nil {
		c.files = map[string][]string{}
	}
	l := strings.Split(string(b), "\n")
	c.files[file] = l
	return l, nil
}

func (c *Checker) checkHelp(r Ref, file, tag string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	for i, line := range lines {
		for _, t := range helpTags(line) {
			if t != tag {
				continue
			}
			if r.Quote == "" || containsQuote(helpSectionText(lines, i), r.Quote) {
				return nil
			}
			return fmt.Errorf("%s: quote not found in the section of *%s* in spec/%s: %q", r, tag, file, r.Quote)
		}
	}
	return fmt.Errorf("%s: no tag *%s* in spec/%s", r, tag, file)
}

// helpSectionText runs from a tag's line to the next tag line that follows some text.
func helpSectionText(lines []string, start int) string {
	end := start + 1
	sawText := false
	for ; end < len(lines) && end < start+helpSection; end++ {
		isTag := len(helpTags(lines[end])) > 0
		if isTag && sawText {
			break
		}
		if !isTag && strings.TrimSpace(lines[end]) != "" {
			sawText = true
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// helpTags returns the tags a help line defines: *tag* bounded by whitespace or the line's ends.
func helpTags(line string) []string {
	var tags []string
	for _, m := range helpTag.FindAllStringSubmatchIndex(line, -1) {
		before := m[0] == 0 || line[m[0]-1] == ' ' || line[m[0]-1] == '\t'
		after := m[1] == len(line) || line[m[1]] == ' ' || line[m[1]] == '\t'
		if before && after {
			tags = append(tags, line[m[2]:m[3]])
		}
	}
	return tags
}

func (c *Checker) checkMarkdown(r Ref, file, heading string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	for i, line := range lines {
		level := len(line) - len(strings.TrimLeft(line, "#"))
		if level == 0 || !strings.HasPrefix(line[level:], " ") || !sameHeading(line[level:], heading) {
			continue
		}
		end := i + 1
		for ; end < len(lines); end++ {
			l := len(lines[end]) - len(strings.TrimLeft(lines[end], "#"))
			if l > 0 && l <= level && strings.HasPrefix(lines[end][l:], " ") {
				break
			}
		}
		if r.Quote == "" || containsQuote(strings.Join(lines[i:end], "\n"), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found under %q in spec/%s: %q", r, heading, file, r.Quote)
	}
	return fmt.Errorf("%s: no heading %q in spec/%s", r, heading, file)
}

func sameHeading(text, want string) bool {
	return strings.EqualFold(strings.TrimSpace(text), strings.TrimSpace(want)) || slug(text) == slug(want)
}

func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	return b.String()
}

// containsQuote matches with runs of whitespace collapsed, since spec text wraps.
func containsQuote(text, quote string) bool {
	return strings.Contains(strings.Join(strings.Fields(text), " "), strings.Join(strings.Fields(quote), " "))
}

// Run checks every reference in .vt files and Go test files under root.
func Run(root string) (checked int, problems []string, err error) {
	c := &Checker{Root: root}
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == "vendor" || d.Name() == "spec" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".vt") && !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		refs, err := Scan(path)
		if err != nil {
			problems = append(problems, err.Error())
			return nil
		}
		for _, r := range refs {
			r.File, _ = filepath.Rel(root, r.File)
			checked++
			if err := c.Check(r); err != nil {
				problems = append(problems, err.Error())
			}
		}
		return nil
	})
	return checked, problems, err
}

var htmlHeading = regexp.MustCompile(`<h([1-6])\b[^>]*\bid=["']([^"']+)["']`)

// checkHTML finds the heading with id and checks the quote in its section, which runs to the next heading
// of the same or a higher level. Tags are dropped and whitespace normalised before matching.
func (c *Checker) checkHTML(r Ref, file, id string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	for i, line := range lines {
		m := htmlHeading.FindStringSubmatch(line)
		if m == nil || m[2] != id {
			continue
		}
		end := i + 1
		for ; end < len(lines); end++ {
			if n := htmlHeading.FindStringSubmatch(lines[end]); n != nil && n[1] <= m[1] {
				break
			}
		}
		if r.Quote == "" || containsQuote(bikeshedText(strings.Join(lines[i:end], "\n")), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in #%s in spec/%s: %q", r, id, file, r.Quote)
	}
	return fmt.Errorf("%s: no heading with id %q in spec/%s", r, id, file)
}

var (
	htmlTag     = regexp.MustCompile(`<[^>]*>`)
	bikeshedVar = regexp.MustCompile(`\|([^|\s]+)\|`)
)

// bikeshedText is a section as a reader sees it: no tags or entities, and Bikeshed's ”value”, |variable| and
// `code` markup gone.
func bikeshedText(s string) string {
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, ""))
	s = strings.NewReplacer("\u00a0", " ", "''", "", "`", "").Replace(s)
	return bikeshedVar.ReplaceAllString(s, "$1")
}

// checkFunc finds `function name(` and checks the quote in its body, up to the next top-level function.
func (c *Checker) checkFunc(r Ref, file, name string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	for i, line := range lines {
		if !strings.HasPrefix(line, "function "+name+"(") && !strings.HasPrefix(line, "function "+name+" (") {
			continue
		}
		end := i + 1
		for ; end < len(lines) && !strings.HasPrefix(lines[end], "function "); end++ {
		}
		if r.Quote == "" || containsQuote(strings.Join(lines[i:end], "\n"), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in function %s in spec/%s: %q", r, name, file, r.Quote)
	}
	return fmt.Errorf("%s: no function %s in spec/%s", r, name, file)
}
