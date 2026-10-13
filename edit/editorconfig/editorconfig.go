// Package editorconfig is an EditorConfig core (spec/editorconfig): the pairs that apply to a file, from every
// .editorconfig between it and the nearest root one, each with where it was set.
package editorconfig

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Pair is a key and its value, and the file and line that set it.
type Pair struct {
	Key, Value string
	File       string
	Line       int
}

// Options change how a file is resolved.
type Options struct {
	Name     string                            // the EditorConfig file name; ".editorconfig" if ""
	Version  string                            // the spec version to behave as; the latest if ""
	ReadFile func(path string) ([]byte, error) // os.ReadFile if nil
}

// knownKeys are the keys whose values are case-insensitive, so they're lowercased.
var knownKeys = map[string]bool{"indent_style": true, "indent_size": true, "tab_width": true, "end_of_line": true,
	"charset": true, "trim_trailing_whitespace": true, "insert_final_newline": true, "root": true}

// File is a parsed EditorConfig file.
type File struct {
	Path     string
	Root     bool
	Sections []Section
	Errors   []error // lines that are none of the line types, skipped
}

type Section struct {
	Glob  string
	Line  int
	Pairs []Pair
}

// Parse reads an EditorConfig file.
func Parse(path string, src []byte) *File {
	f := &File{Path: path}
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var cur *Section
	n := 0
	for sc.Scan() {
		n++
		l := strings.TrimSpace(sc.Text())
		switch {
		case l == "" || l[0] == ';' || l[0] == '#':
		case l[0] == '[' && l[len(l)-1] == ']':
			f.Sections = append(f.Sections, Section{Glob: l[1 : len(l)-1], Line: n})
			cur = &f.Sections[len(f.Sections)-1]
		case strings.Contains(l, "="):
			k, v, _ := strings.Cut(l, "=")
			p := Pair{Key: strings.ToLower(strings.TrimSpace(k)), Value: strings.TrimSpace(v), File: path, Line: n}
			if knownKeys[p.Key] {
				p.Value = strings.ToLower(p.Value)
			}
			if cur == nil {
				if p.Key == "root" {
					f.Root = p.Value == "true"
				}
				continue
			}
			cur.Pairs = append(cur.Pairs, p)
		default:
			f.Errors = append(f.Errors, errors.New(path+":"+strconv.Itoa(n)+": not a section, pair or comment"))
		}
	}
	return f
}

// Result is what applies to a file: its pairs, in the order they were first set; lines that couldn't be read; and
// every EditorConfig file looked for, so a change to one (or a new one) can be noticed.
type Result struct {
	Pairs    []Pair
	Problems []error
	Looked   []string
}

// Resolve gives what applies to the file at path (absolute), the closest file's pairs winning.
func Resolve(path string, o Options) (Result, error) {
	var res Result
	name := o.Name
	if name == "" {
		name = ".editorconfig"
	}
	read := o.ReadFile
	if read == nil {
		read = os.ReadFile
	}
	path = filepath.ToSlash(filepath.Clean(path))
	var files []*File
	for dir := filepath.Dir(filepath.FromSlash(path)); ; {
		b, err := read(filepath.Join(dir, name))
		res.Looked = append(res.Looked, filepath.Join(dir, name))
		switch {
		case err == nil:
			f := Parse(filepath.Join(dir, name), b)
			files = append(files, f)
			res.Problems = append(res.Problems, f.Errors...)
			if f.Root {
				dir = ""
			}
		case !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission):
			return res, err
		}
		parent := filepath.Dir(dir)
		if dir == "" || parent == dir {
			break
		}
		dir = parent
	}
	var out []Pair
	at := map[string]int{}
	for i := len(files) - 1; i >= 0; i-- {
		f := files[i]
		dir := filepath.ToSlash(filepath.Dir(f.Path))
		for _, s := range f.Sections {
			if !Match(s.Glob, dir, path) {
				continue
			}
			for _, p := range s.Pairs {
				if j, ok := at[p.Key]; ok {
					out[j] = p
					continue
				}
				at[p.Key] = len(out)
				out = append(out, p)
			}
		}
	}
	res.Pairs = defaults(out, o.Version)
	return res, nil
}

// defaults fills in what the spec derives: indent_size from indent_style tab, tab_width from indent_size, and
// indent_size tab as tab_width. A pair set to "unset" is reported as it is; the editor treats it as no value.
func defaults(ps []Pair, version string) []Pair {
	get := func(k string) (int, bool) {
		for i, p := range ps {
			if p.Key == k {
				return i, true
			}
		}
		return -1, false
	}
	style, hasStyle := get("indent_style")
	size, hasSize := get("indent_size")
	if hasStyle && ps[style].Value == "tab" && !hasSize && !older(version, 0, 10, 0) {
		ps = append(ps, Pair{Key: "indent_size", Value: "tab", File: ps[style].File, Line: ps[style].Line})
		size, hasSize = len(ps)-1, true
	}
	width, hasWidth := get("tab_width")
	if hasSize && !hasWidth && ps[size].Value != "tab" {
		ps = append(ps, Pair{Key: "tab_width", Value: ps[size].Value, File: ps[size].File, Line: ps[size].Line})
	}
	if hasSize && hasWidth && ps[size].Value == "tab" {
		ps[size].Value = ps[width].Value
	}
	return ps
}

// older reports whether version is before major.minor.patch.
func older(version string, major, minor, patch int) bool {
	if version == "" {
		return false
	}
	var v [3]int
	for i, s := range strings.SplitN(version, ".", 3) {
		v[i], _ = strconv.Atoi(s)
	}
	w := [3]int{major, minor, patch}
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}
	return false
}

// Match reports whether glob, a section name in the EditorConfig file in dir, matches path.
func Match(glob, dir, path string) bool {
	re, nums, ok := compile(glob, dir)
	if !ok {
		return false
	}
	m := re.FindStringSubmatch(path)
	if m == nil {
		return false
	}
	for i, r := range nums {
		n, err := strconv.Atoi(m[i+1])
		if err != nil || n < r[0] || n > r[1] {
			return false
		}
	}
	return true
}

// compile turns a glob into a regular expression over whole paths, with a numeric range to check for each group.
func compile(glob, dir string) (*regexp.Regexp, [][2]int, bool) {
	if strings.HasSuffix(glob, "/") {
		return nil, nil, false
	}
	hasSlash := false
	for i, depth := 0, 0; i < len(glob); i++ {
		switch glob[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth = max(0, depth-1)
		case '/':
			if depth == 0 {
				hasSlash = true
			}
		}
	}
	prefix := regexp.QuoteMeta(strings.TrimSuffix(dir, "/")) + "/"
	switch {
	case strings.HasPrefix(glob, "/"):
		glob = glob[1:]
	case !hasSlash:
		prefix += "(?:.*/)?"
	}
	g := &globber{s: glob}
	body := g.alt(false)
	re, err := regexp.Compile("^" + prefix + body + "$")
	if err != nil {
		return nil, nil, false
	}
	return re, g.nums, true
}

type globber struct {
	s    string
	i    int
	nums [][2]int
}

// braces finds the } that closes the { at i, and whether a comma (or ..) sits at its top level.
func (g *globber) braces(i int) (end int, alt bool) {
	depth := 0
	for j := i; j < len(g.s); j++ {
		switch g.s[j] {
		case '\\':
			j++
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j, alt
			}
		case ',':
			if depth == 1 {
				alt = true
			}
		}
	}
	return -1, false
}

var rangeRe = regexp.MustCompile(`^\{([+-]?\d+)\.\.([+-]?\d+)\}$`)

// alt translates until the end, or (inside braces) a top-level , or }.
func (g *globber) alt(inBrace bool) string {
	var b strings.Builder
	for g.i < len(g.s) {
		c := g.s[g.i]
		switch {
		case inBrace && (c == ',' || c == '}'):
			return b.String()
		case c == '\\' && g.i+1 < len(g.s):
			b.WriteString(regexp.QuoteMeta(g.s[g.i+1 : g.i+2]))
			g.i += 2
		case c == '*' && strings.HasPrefix(g.s[g.i:], "**"):
			g.i += 2
			if strings.HasSuffix(b.String(), "/") && strings.HasPrefix(g.s[g.i:], "/") {
				// a/**/b also matches a/b
				g.i++
				b.WriteString("(?:.*/)?")
				continue
			}
			b.WriteString(".*")
		case c == '*':
			g.i++
			b.WriteString("[^/]*")
		case c == '?':
			g.i++
			b.WriteString("[^/]")
		case c == '[':
			b.WriteString(g.class())
		case c == '{':
			end, isAlt := g.braces(g.i)
			if m := rangeRe.FindStringSubmatch(g.s[g.i:max(end+1, g.i)]); end > 0 && m != nil {
				lo, _ := strconv.Atoi(m[1])
				hi, _ := strconv.Atoi(m[2])
				g.nums = append(g.nums, [2]int{lo, hi})
				b.WriteString(`([+-]?(?:0|[1-9]\d*))`)
				g.i = end + 1
				continue
			}
			switch {
			case end < 0:
				b.WriteString(regexp.QuoteMeta("{"))
				g.i++
				continue
			case !isAlt: // {s1} is only itself
				b.WriteString(regexp.QuoteMeta(g.s[g.i : end+1]))
				g.i = end + 1
				continue
			}
			g.i++
			var opts []string
			for {
				opts = append(opts, g.alt(true))
				if g.i >= len(g.s) || g.s[g.i] == '}' {
					g.i++
					break
				}
				g.i++ // the comma
			}
			b.WriteString("(?:" + strings.Join(opts, "|") + ")")
		default:
			_, n := utf8.DecodeRuneInString(g.s[g.i:])
			b.WriteString(regexp.QuoteMeta(g.s[g.i : g.i+n]))
			g.i += n
		}
	}
	return b.String()
}

// class translates [seq] or [!seq]; one with a / in it, or unclosed, is literal.
func (g *globber) class() string {
	j := g.i + 1
	if j < len(g.s) && (g.s[j] == '!' || g.s[j] == '^') {
		j++
	}
	if j < len(g.s) && g.s[j] == ']' {
		j++
	}
	for j < len(g.s) && g.s[j] != ']' {
		if g.s[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(g.s) || strings.Contains(g.s[g.i:j], "/") {
		g.i++
		return regexp.QuoteMeta("[")
	}
	body := g.s[g.i+1 : j]
	g.i = j + 1
	var b strings.Builder
	b.WriteByte('[')
	if body != "" && body[0] == '!' {
		b.WriteByte('^')
		body = body[1:]
	}
	for k := 0; k < len(body); k++ {
		c := body[k]
		switch {
		case c == '\\' && k+1 < len(body):
			k++
			b.WriteString(regexp.QuoteMeta(body[k : k+1]))
		case c == '-':
			b.WriteByte('-')
		case strings.IndexByte(`\[]^`, c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			_, n := utf8.DecodeRuneInString(body[k:])
			b.WriteString(body[k : k+n])
			k += n - 1
		}
	}
	b.WriteByte(']')
	return b.String()
}
