// Package attr reads gitattributes(5): the attributes git gives a path, from the .gitattributes files between it
// and the top of the work tree and $GIT_DIR/info/attributes, with git's pattern rules (gitignore(5)) and macros.
package attr

import (
	"bufio"
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Value is an attribute's state for a path: set, unset, or a string; the zero Value is unspecified.
type Value struct {
	Set, Unset bool
	Text       string
	File       string // the file and line that gave it
	Line       int
}

// Specified reports whether a pattern gave the attribute a state.
func (v Value) Specified() bool { return v.Set || v.Unset || v.Text != "" }

type assign struct {
	name  string
	value Value // zero: "!name", back to unspecified
}

type line struct {
	re    *regexp.Regexp
	attrs []assign
	num   int
}

type file struct {
	path   string
	dir    string // the directory its patterns are relative to, from the work tree's top; "" at the top
	lines  []line
	macros map[string][]assign
}

// Repo reads a work tree's attributes files as they're needed, again when they change.
type Repo struct {
	root  string
	mu    sync.Mutex
	files map[string]*cached
}

type cached struct {
	f    *file
	stat os.FileInfo
}

// Open is the work tree at root.
func Open(root string) *Repo { return &Repo{root: root, files: map[string]*cached{}} }

// builtin is git's own macro.
var builtin = map[string][]assign{"binary": {{"diff", Value{Unset: true}}, {"merge", Value{Unset: true}}, {"text", Value{Unset: true}}}}

// Files are the attributes files that bear on rel, from the highest precedence: $GIT_DIR/info/attributes, then the
// .gitattributes closest to rel, up to the top of the work tree.
func (r *Repo) Files(rel string) []string {
	out := []string{filepath.Join(r.root, ".git", "info", "attributes")}
	for d := path.Dir(rel); ; d = path.Dir(d) {
		if d == "." {
			d = ""
		}
		out = append(out, filepath.Join(r.root, filepath.FromSlash(d), ".gitattributes"))
		if d == "" {
			return out
		}
	}
}

// Attributes are every attribute specified for rel, a path from the work tree's top.
func (r *Repo) Attributes(rel string) (map[string]Value, error) {
	rel = path.Clean(filepath.ToSlash(rel))
	var stack []*file
	macros := map[string][]assign{}
	for k, v := range builtin {
		macros[k] = v
	}
	paths := r.Files(rel)
	for i, p := range paths {
		f, err := r.load(p)
		if err != nil {
			return nil, err
		}
		if f == nil {
			continue
		}
		stack = append(stack, f)
		// Macros come only from the top-level file and info/attributes.
		if i == 0 || i == len(paths)-1 {
			for k, v := range f.macros {
				if _, ok := macros[k]; !ok || i == 0 {
					macros[k] = v
				}
			}
		}
	}
	got := map[string]Value{}
	done := map[string]bool{}
	var fill func(a assign, src Value)
	fill = func(a assign, src Value) {
		if done[a.name] {
			return
		}
		done[a.name] = true
		v := a.value
		v.File, v.Line = src.File, src.Line
		if v.Specified() {
			got[a.name] = v
		}
		if m, ok := macros[a.name]; ok && a.value.Set {
			for i := len(m) - 1; i >= 0; i-- {
				fill(m[i], src)
			}
		}
	}
	for _, f := range stack {
		for i := len(f.lines) - 1; i >= 0; i-- {
			l := f.lines[i]
			if !l.re.MatchString(rel) {
				continue
			}
			for j := len(l.attrs) - 1; j >= 0; j-- {
				fill(l.attrs[j], Value{File: f.path, Line: l.num})
			}
		}
	}
	return got, nil
}

func (r *Repo) load(p string) (*file, error) {
	st, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) || err == nil && st.IsDir() {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.files[p]; ok && c.stat.ModTime().Equal(st.ModTime()) && c.stat.Size() == st.Size() {
		return c.f, nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	dir := ""
	if filepath.Base(p) == ".gitattributes" {
		rel, _ := filepath.Rel(r.root, filepath.Dir(p))
		if rel != "." {
			dir = filepath.ToSlash(rel)
		}
	}
	f := parse(p, dir, b)
	r.files[p] = &cached{f, st}
	return f, nil
}

func parse(p, dir string, src []byte) *file {
	f := &file{path: p, dir: dir, macros: map[string][]assign{}}
	sc := bufio.NewScanner(bytes.NewReader(src))
	n := 0
	for sc.Scan() {
		n++
		l := strings.TrimSpace(sc.Text())
		if l == "" || l[0] == '#' {
			continue
		}
		pat, rest := splitPattern(l)
		attrs := parseAttrs(rest)
		if name, ok := strings.CutPrefix(pat, "[attr]"); ok {
			f.macros[name] = attrs
			continue
		}
		if pat == "" || pat[0] == '!' {
			continue // negative patterns are forbidden
		}
		if re := compile(pat, dir); re != nil {
			f.lines = append(f.lines, line{re: re, attrs: attrs, num: n})
		}
	}
	return f
}

// splitPattern takes the pattern off a line: up to whitespace, or a C-quoted string.
func splitPattern(l string) (string, string) {
	if l[0] == '"' {
		for i := 1; i < len(l); i++ {
			switch l[i] {
			case '\\':
				i++
			case '"':
				if s, err := strconv.Unquote(l[:i+1]); err == nil {
					return s, l[i+1:]
				}
				return l[1:i], l[i+1:]
			}
		}
	}
	i := strings.IndexAny(l, " \t")
	if i < 0 {
		return l, ""
	}
	return l[:i], l[i:]
}

func parseAttrs(s string) []assign {
	var out []assign
	for _, a := range strings.Fields(s) {
		switch {
		case strings.HasPrefix(a, "-"):
			out = append(out, assign{a[1:], Value{Unset: true}})
		case strings.HasPrefix(a, "!"):
			out = append(out, assign{a[1:], Value{}})
		case strings.Contains(a, "="):
			k, v, _ := strings.Cut(a, "=")
			out = append(out, assign{k, Value{Text: v}})
		default:
			out = append(out, assign{a, Value{Set: true}})
		}
	}
	return out
}

// compile turns a gitignore(5) pattern in the file at dir into a regular expression over paths from the top. A
// pattern for directories (a trailing /) never matches a file, so it's dropped.
func compile(pat, dir string) *regexp.Regexp {
	if strings.HasSuffix(pat, "/") {
		return nil
	}
	anchored := strings.Contains(strings.TrimPrefix(pat, "/"), "/") || strings.HasPrefix(pat, "/")
	pat = strings.TrimPrefix(pat, "/")
	prefix := ""
	if dir != "" {
		prefix = regexp.QuoteMeta(dir) + "/"
	}
	var b strings.Builder
	b.WriteString("^" + prefix)
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(pat); i++ {
		c := pat[i]
		switch {
		case c == '\\' && i+1 < len(pat):
			i++
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
		case c == '*' && strings.HasPrefix(pat[i:], "**"):
			atStart := i == 0
			afterSlash := i > 0 && pat[i-1] == '/'
			end := i + 2
			switch {
			case atStart && strings.HasPrefix(pat[end:], "/"):
				b.WriteString("(?:.*/)?")
				i = end
			case afterSlash && end == len(pat):
				b.WriteString(".*")
				i = end - 1
			case afterSlash && strings.HasPrefix(pat[end:], "/"):
				b.WriteString("(?:.*/)?")
				i = end
			default:
				for i+1 < len(pat) && pat[i+1] == '*' {
					i++
				}
				b.WriteString("[^/]*")
			}
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		case c == '[':
			j := i + 1
			if j < len(pat) && (pat[j] == '!' || pat[j] == '^') {
				j++
			}
			if j < len(pat) && pat[j] == ']' {
				j++
			}
			for j < len(pat) && pat[j] != ']' {
				j++
			}
			if j >= len(pat) {
				b.WriteString(`\[`)
				continue
			}
			cls := pat[i+1 : j]
			if cls != "" && (cls[0] == '!' || cls[0] == '^') {
				cls = "^" + cls[1:]
			}
			b.WriteString("[" + strings.ReplaceAll(cls, `\`, `\\`) + "]")
			i = j
		default:
			b.WriteString(regexp.QuoteMeta(pat[i : i+1]))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil
	}
	return re
}
