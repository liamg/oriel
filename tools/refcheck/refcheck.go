// Package refcheck verifies that test references resolve in the vendored specs under spec/,
// and that their quotes appear there verbatim.
//
// A reference is a "ref:" line, optionally followed by a "quote:" line, in a .vt file or a Go test comment:
//
//	ref:    motion.txt *w*              vim help tag (spec/vim)
//	ref:    nvim/motion.txt *w*         nvim help tag (spec/nvim)
//	ref:    lsp/specification.md#Text Documents   markdown heading, by its text or its <a name="…"> anchor
//	ref:    css-color-4/Overview.bs#hue-interpolation   HTML or Bikeshed section, by its heading's id
//	ref:    css-color-4/conversions.js#OKLab_to_XYZ     a function in vendored sample code
//	ref:    testdir/test_normal.vim#Test_normal_dw      a test function in vim's vendored tests (spec/vim)
//	ref:    go/src/testing/match.go#rewrite   a Go declaration (func, method as T.name, type, var, const)
//	ref:    go/help-testflag.txt        a whole file, for text with no sections (a tool's help output)
//	ref:    xterm/ctlseqs.txt CSI Ps ; Ps H             an entry of a plain-text definition list
//	ref:    unicode/tr29.html#GB11                      also an <a name> anchor; an .mdx/.md front-matter title names the file
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
	if file, entry, ok := strings.Cut(ref, " "); ok && strings.HasSuffix(file, ".txt") && !isHelpTag(strings.TrimSpace(entry)) {
		return c.checkEntry(r, file, strings.TrimSpace(entry))
	}
	if file, heading, ok := strings.Cut(ref, "#"); ok {
		switch filepath.Ext(file) {
		case ".md", ".mdx":
			return c.checkMarkdown(r, file, heading)
		case ".bs", ".html":
			return c.checkHTML(r, file, heading)
		case ".js":
			return c.checkFunc(r, file, heading)
		case ".vim":
			return c.checkVimFunc(r, file, heading)
		case ".go":
			return c.checkGoDecl(r, file, heading)
		case ".rst", ".txt":
			return c.checkUnderlined(r, file, heading)
		}
	}
	file, anchor, ok := strings.Cut(ref, " ")
	anchor = strings.TrimSpace(anchor)
	if ok && strings.HasSuffix(file, ".txt") && isHelpTag(anchor) {
		if !strings.Contains(file, "/") {
			file = "vim/" + file
		}
		return c.checkHelp(r, file, strings.Trim(anchor, "*"))
	}
	if !strings.ContainsAny(ref, " \t#") && fileExists(filepath.Join(c.Root, "spec", filepath.FromSlash(ref))) {
		return c.checkWhole(r, ref)
	}
	return fmt.Errorf("%s: unrecognised ref %q", r, ref)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// checkWhole checks the quote anywhere in a file.
func (c *Checker) checkWhole(r Ref, file string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	if r.Quote == "" || containsQuote(strings.Join(lines, "\n"), r.Quote) {
		return nil
	}
	return fmt.Errorf("%s: quote not found in spec/%s: %q", r, file, r.Quote)
}

func isHelpTag(s string) bool {
	return len(s) > 2 && strings.HasPrefix(s, "*") && strings.HasSuffix(s, "*")
}

// checkEntry finds an entry of a definition list like xterm's ctlseqs.txt: a line starting at column 0 with the
// entry, then its description, on the same line or indented below. The section runs to the next column-0 line after
// some description, so entries listed together share one. An entry alone between blank lines is a heading, whose
// section runs to the next such line. Any entry with that text may hold the quote.
func (c *Checker) checkEntry(r Ref, file, entry string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	found := false
	for i, line := range lines {
		rest, ok := strings.CutPrefix(line, entry)
		if !ok || !entryEnds(rest) {
			continue
		}
		found = true
		if r.Quote == "" || containsQuote(strings.Join(lines[i:entryEnd(lines, i, rest)], "\n"), r.Quote) {
			return nil
		}
	}
	if !found {
		return fmt.Errorf("%s: no entry %q in spec/%s", r, entry, file)
	}
	return fmt.Errorf("%s: quote not found under %q in spec/%s: %q", r, entry, file, r.Quote)
}

// entryEnd is where the section of the entry on line i ends; rest is what follows the entry on its line.
func entryEnd(lines []string, i int, rest string) int {
	end := i + 1
	if isTextHeading(lines, i) {
		for ; end < len(lines) && !isTextHeading(lines, end); end++ {
		}
		return end
	}
	sawText := strings.TrimSpace(rest) != ""
	for ; end < len(lines); end++ {
		l := lines[end]
		if l != "" && l[0] != ' ' && l[0] != '\t' {
			if sawText {
				break
			}
			continue
		}
		if strings.TrimSpace(l) != "" {
			sawText = true
		}
	}
	return end
}

// isTextHeading reports whether line i is alone between blank lines, at column 0 with no two-space gap.
func isTextHeading(lines []string, i int) bool {
	l := lines[i]
	return l != "" && l[0] != ' ' && l[0] != '\t' && !strings.Contains(l, "  ") &&
		(i == 0 || strings.TrimSpace(lines[i-1]) == "") && (i+1 == len(lines) || strings.TrimSpace(lines[i+1]) == "")
}

// entryEnds reports whether an entry's line goes on with its description: nothing, two spaces, or one space before a
// capitalised word.
func entryEnds(rest string) bool {
	switch {
	case rest == "", strings.HasPrefix(rest, "  "):
		return true
	case len(rest) > 2 && rest[0] == ' ':
		return rest[1] >= 'A' && rest[1] <= 'Z' && rest[2] >= 'a' && rest[2] <= 'z'
	}
	return false
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
	if title, ok := frontMatterTitle(lines); ok && sameHeading(title, heading) {
		if r.Quote == "" || containsQuote(strings.Join(lines, "\n"), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in spec/%s: %q", r, file, r.Quote)
	}
	for i, line := range lines {
		level := len(line) - len(strings.TrimLeft(line, "#"))
		if level == 0 || !strings.HasPrefix(line[level:], " ") || !markdownHeading(line[level:], heading) {
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
	return c.checkUnderlined(r, file, heading) // a setext heading, underlined with = or -
}

var mdAnchor = regexp.MustCompile(`<a\b[^>]*\bname=["']([^"']+)["']`)

// markdownHeading matches a heading by its text, with or without what looks like HTML tags (an <a> anchor, or a
// placeholder such as <regex>), or by the name of an anchor in it.
func markdownHeading(line, want string) bool {
	if m := mdAnchor.FindStringSubmatch(line); m != nil && m[1] == strings.TrimSpace(want) {
		return true
	}
	return sameHeading(line, want) || sameHeading(htmlTag.ReplaceAllString(line, ""), want)
}

// frontMatterTitle is the title: of a YAML front matter block, which heads the whole file.
func frontMatterTitle(lines []string) (string, bool) {
	if len(lines) == 0 || lines[0] != "---" {
		return "", false
	}
	for _, l := range lines[1:] {
		if l == "---" {
			break
		}
		if t, ok := strings.CutPrefix(l, "title:"); ok {
			return strings.Trim(strings.TrimSpace(t), `"'`), true
		}
	}
	return "", false
}

// underline is a reStructuredText or AsciiDoc heading's underline: one punctuation character, repeated.
func underline(l string) (byte, bool) {
	l = strings.TrimRight(l, " ")
	if len(l) < 3 || !strings.ContainsRune("=-~^\"'*+#", rune(l[0])) {
		return 0, false
	}
	return l[0], strings.Count(l, l[:1]) == len(l)
}

// checkUnderlined finds a heading underlined in reStructuredText or AsciiDoc and checks the quote in its section, up
// to the next heading as high. A heading's level is the order its underline character first appears in.
func (c *Checker) checkUnderlined(r Ref, file, heading string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	levels := map[byte]int{}
	level := func(i int) int {
		if i+1 >= len(lines) || strings.TrimSpace(lines[i]) == "" {
			return 0
		}
		ch, ok := underline(lines[i+1])
		if !ok {
			return 0
		}
		if _, seen := levels[ch]; !seen {
			levels[ch] = len(levels) + 1
		}
		return levels[ch]
	}
	clean := func(s string) string { return strings.ReplaceAll(s, "`", "") }
	for i := range lines {
		lv := level(i)
		if lv == 0 || !sameHeading(clean(lines[i]), heading) {
			continue
		}
		end := i + 2
		for ; end < len(lines); end++ {
			if l := level(end); l > 0 && l <= lv {
				break
			}
		}
		if r.Quote == "" || containsQuote(clean(strings.Join(lines[i:end], "\n")), r.Quote) {
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

var (
	htmlHeading       = regexp.MustCompile(`<h([1-6])\b[^>]*\bid=["']([^"']+)["']`)
	htmlHeadingAnchor = regexp.MustCompile(`<h([1-6])\b[^>]*>.*?<a\b[^>]*\bname=["']([^"']+)["']`)
)

// heading is a line's heading level and id, or its first <a name> anchor's name.
func heading(line string) (level, id string, ok bool) {
	m := htmlHeading.FindStringSubmatch(line)
	if m == nil {
		m = htmlHeadingAnchor.FindStringSubmatch(line)
	}
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// checkHTML finds the heading with id (or holding an <a name> anchor) and checks the quote in its section, which runs
// to the next such heading of the same or a higher level. Tags are dropped and whitespace normalised before matching.
func (c *Checker) checkHTML(r Ref, file, id string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	for i, line := range lines {
		level, hid, ok := heading(line)
		if !ok || hid != id {
			continue
		}
		end := i + 1
		for ; end < len(lines); end++ {
			if l, _, ok := heading(lines[end]); ok && l <= level {
				break
			}
		}
		if r.Quote == "" || containsQuote(bikeshedText(strings.Join(lines[i:end], "\n")), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in #%s in spec/%s: %q", r, id, file, r.Quote)
	}
	if text, ok := elementText(strings.Join(lines, "\n"), id); ok {
		if r.Quote == "" || containsQuote(bikeshedText(text), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in #%s in spec/%s: %q", r, id, file, r.Quote)
	}
	return fmt.Errorf("%s: no heading or element with id %q in spec/%s", r, id, file)
}

var blockEnd = regexp.MustCompile(`(?i)</(p|tr|li|dd)>`)

// elementText is the element with id, to its closing tag; a <dfn>'s runs on through its definition, the <dd> after it.
// An <a name> anchor's runs to the end of the paragraph, table row, list item or definition holding it.
func elementText(src, id string) (string, bool) {
	m := regexp.MustCompile(`<([a-zA-Z][a-zA-Z0-9]*)\b[^>]*\bid=["']` + regexp.QuoteMeta(id) + `["'][^>]*>`).FindStringSubmatchIndex(src)
	if m == nil {
		a := regexp.MustCompile(`<a\b[^>]*\bname=["']` + regexp.QuoteMeta(id) + `["']`).FindStringIndex(src)
		if a == nil {
			return "", false
		}
		if e := blockEnd.FindStringIndex(src[a[0]:]); e != nil {
			return src[a[0] : a[0]+e[0]], true
		}
		return src[a[0]:], true
	}
	tag := strings.ToLower(src[m[2]:m[3]])
	if tag == "dfn" {
		if end := strings.Index(src[m[0]:], "</dd>"); end >= 0 {
			return src[m[0] : m[0]+end], true
		}
		return src[m[0]:], true
	}
	open := regexp.MustCompile(`(?i)<(/?)` + tag + `\b[^>]*>`)
	depth := 0
	for _, t := range open.FindAllStringSubmatchIndex(src[m[0]:], -1) {
		if t[3] > t[2] {
			depth--
		} else {
			depth++
		}
		if depth == 0 {
			return src[m[0] : m[0]+t[0]], true
		}
	}
	return src[m[0]:], true
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

var vimFunc = regexp.MustCompile(`^fu(?:n|nc|nct|ncti|nctio|nction)?!?\s+([\w:#]+)\s*\(`)

// checkVimFunc finds `func name(` in a vendored Vimscript file (under spec/vim unless the path says
// otherwise) and checks the quote in its body, up to its `endfunc`.
func (c *Checker) checkVimFunc(r Ref, file, name string) error {
	if !strings.HasPrefix(file, "vim/") && !strings.HasPrefix(file, "nvim/") {
		file = "vim/" + file
	}
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	for i, line := range lines {
		if m := vimFunc.FindStringSubmatch(line); m == nil || m[1] != name {
			continue
		}
		end := i + 1
		for ; end < len(lines) && !strings.HasPrefix(lines[end], "endf"); end++ {
		}
		if r.Quote == "" || containsQuote(strings.Join(lines[i:min(end+1, len(lines))], "\n"), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in function %s in spec/%s: %q", r, name, file, r.Quote)
	}
	return fmt.Errorf("%s: no function %s in spec/%s", r, name, file)
}

var goDecl = regexp.MustCompile(`^(?:func\s+(?:\(\s*\w*\s*\*?\s*(\w+)(?:\[[^\]]*\])?\s*\)\s*)?(\w+)\s*[\[(]|(?:type|var|const)\s+(\w+)\b)`)

// checkGoDecl finds a top-level Go declaration, name or Recv.name for a method, and checks the quote in it and its
// doc comment, up to the next top-level declaration.
func (c *Checker) checkGoDecl(r Ref, file, name string) error {
	lines, err := c.lines(file)
	if err != nil {
		return fmt.Errorf("%s: %v", r, err)
	}
	recv, fn, isMethod := strings.Cut(name, ".")
	if !isMethod {
		recv, fn = "", name
	}
	decl := func(l string) bool { return goDecl.MatchString(l) }
	for i, line := range lines {
		m := goDecl.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		got, gotRecv := m[2], m[1]
		if got == "" {
			got = m[3]
		}
		if got != fn || isMethod && gotRecv != recv {
			continue
		}
		start := i
		for start > 0 && strings.HasPrefix(lines[start-1], "//") {
			start--
		}
		end := i + 1
		for ; end < len(lines) && !decl(lines[end]); end++ {
		}
		for end > i+1 && strings.HasPrefix(lines[end-1], "//") {
			end--
		}
		if r.Quote == "" || containsQuote(goCommentText(lines[start:end]), r.Quote) {
			return nil
		}
		return fmt.Errorf("%s: quote not found in %s in spec/%s: %q", r, name, file, r.Quote)
	}
	return fmt.Errorf("%s: no declaration %s in spec/%s", r, name, file)
}

// goCommentText is Go source with each line's comment marker dropped, so a quote can run across comment lines.
func goCommentText(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimPrefix(strings.TrimSpace(l), "//")
	}
	return strings.Join(out, "\n")
}
