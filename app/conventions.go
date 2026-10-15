package app

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/edit/editorconfig"
	"github.com/liamg/oriel/gitx/attr"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim/vimreg"
)

// conventionsEvery is how often the files a buffer's conventions come from are read again.
const conventionsEvery = 500 * time.Millisecond

// conventions are each buffer's own: .editorconfig, the IDE-relevant .gitattributes and, when on, modelines, applied
// as the registry's file layer, under :set (ED-9).
type conventions struct {
	h       plugin.Host
	buffers func() []string // the names of the open buffers with files
	dir     string          // relative names are under it; sources are shown relative to it
	reposMu sync.Mutex      // project search reads repos off the loop
	repos   map[string]*attr.Repo
	applied map[string]map[string]any // by buffer name: what the file layer holds
	warned  map[string]bool
	last    time.Time
}

func newConventions(h plugin.Host, dir string, buffers func() []string) *conventions {
	return &conventions{h: h, dir: dir, buffers: buffers, repos: map[string]*attr.Repo{}, applied: map[string]map[string]any{}, warned: map[string]bool{}}
}

// options are the conventions' own options, besides the engine's.
func conventionOptions() []registry.OptionSpec {
	file := registry.ScopeUser | registry.ScopeProject | registry.ScopeSession | registry.ScopeBuffer
	return []registry.OptionSpec{
		{Name: "editor.end_of_line", Section: "editor", Type: registry.Enum, Values: []string{"keep", "lf", "crlf"}, Default: "keep", Scopes: file,
			Help: "Line endings on save: keep the file's, or lf or crlf."},
		{Name: "editor.charset", Section: "editor", Type: registry.Enum, Values: []string{"keep", "utf-8", "utf-8-bom"}, Default: "keep", Scopes: file,
			Help: "The file's encoding on save: keep its byte-order mark or not, or utf-8 without one, or utf-8-bom with."},
		{Name: "editor.trim_trailing_whitespace", Section: "editor", Type: registry.Bool, Default: false, Scopes: file,
			Help: "Remove white space at the ends of lines on save."},
		{Name: "editor.insert_final_newline", Section: "editor", Type: registry.Bool, Default: true, Scopes: file,
			Help: "End the file with a newline on save; off writes none after the last line."},
		{Name: "editor.max_line_length", Section: "editor", Type: registry.Int, Default: 0, Min: 0, Max: 10000, Scopes: file,
			Help: "The line length the project keeps to; 0 for none."},
		{Name: "editor.binary", Section: "editor", Type: registry.Bool, Default: false, Scopes: file,
			Help: "The file isn't text (.gitattributes' binary or -text): it opens read-only."},
		{Name: "files.generated", Section: "files", Type: registry.Bool, Default: false, Scopes: file,
			Help: "The file is generated or vendored (.gitattributes' linguist-generated, linguist-vendored)."},
		{Name: "editor.modelines", Section: "editor", Type: registry.Bool, Default: false, Scopes: registry.ScopeUser | registry.ScopeProject,
			Help: "Read vim modelines from the first and last five lines of a file: tab stop, shift width and expand tab only."},
	}
}

// value is an option's value and where it came from.
type value struct {
	v   any
	src string
}

func (c *conventions) rel(p string) string {
	if r, err := filepath.Rel(c.dir, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func (c *conventions) abs(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(c.dir, name)
}

// repo is the git work tree path is in, or the work dir when it's in none.
func (c *conventions) repo(path string) (*attr.Repo, string) {
	root := c.dir
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		if st, err := os.Stat(filepath.Join(d, ".git")); err == nil && st.IsDir() {
			root = d
			break
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	c.reposMu.Lock()
	r, ok := c.repos[root]
	if !ok {
		r = attr.Open(root)
		c.repos[root] = r
	}
	c.reposMu.Unlock()
	rel, _ := filepath.Rel(root, path)
	return r, filepath.ToSlash(rel)
}

// resolve is everything a file's conventions set: .gitattributes, then .editorconfig over it, then modelines.
func (c *conventions) resolve(path string) map[string]value {
	out := map[string]value{}
	set := func(name string, v any, src string) { out[name] = value{v, src} }
	at := func(file string, line int) string { return fmt.Sprintf("%s:%d", c.rel(file), line) }

	if r, rel := c.repo(path); !strings.HasPrefix(rel, "..") {
		attrs, err := r.Attributes(rel)
		if err == nil {
			if v := attrs["eol"]; v.Text == "lf" || v.Text == "crlf" {
				set("editor.end_of_line", v.Text, at(v.File, v.Line))
			}
			if v := attrs["text"]; v.Unset {
				set("editor.binary", true, at(v.File, v.Line))
			}
			if v := attrs["working-tree-encoding"]; v.Text != "" {
				c.charset(strings.ToLower(v.Text), at(v.File, v.Line), set)
			}
			for _, k := range []string{"linguist-vendored", "linguist-generated"} {
				if v := attrs[k]; v.Set || v.Text == "true" {
					set("files.generated", true, at(v.File, v.Line))
				} else if v.Unset || v.Text == "false" {
					set("files.generated", false, at(v.File, v.Line))
				}
			}
		}
	}

	res, _ := editorconfig.Resolve(path, editorconfig.Options{})
	pairs := map[string]editorconfig.Pair{}
	for _, p := range res.Pairs {
		if p.Value != "unset" {
			pairs[p.Key] = p
		}
	}
	num := func(k string) (int, bool) {
		n, err := strconv.Atoi(pairs[k].Value)
		return n, err == nil && n > 0
	}
	src := func(k string) string { return at(pairs[k].File, pairs[k].Line) }
	switch pairs["indent_style"].Value {
	case "tab":
		set("editor.expand_tab", false, src("indent_style"))
	case "space":
		set("editor.expand_tab", true, src("indent_style"))
	}
	if n, ok := num("tab_width"); ok {
		set("editor.tab_stop", n, src("tab_width"))
	}
	if n, ok := num("indent_size"); ok {
		set("editor.shift_width", n, src("indent_size"))
	}
	switch v := pairs["end_of_line"].Value; v {
	case "lf", "crlf":
		set("editor.end_of_line", v, src("end_of_line"))
	case "cr":
		c.warn("cr", "Oriel writes lf or crlf line endings, not cr ("+src("end_of_line")+").")
	}
	if p, ok := pairs["charset"]; ok {
		c.charset(p.Value, src("charset"), set)
	}
	for _, k := range []string{"trim_trailing_whitespace", "insert_final_newline"} {
		if v := pairs[k].Value; v == "true" || v == "false" {
			set("editor."+k, v == "true", src(k))
		}
	}
	if v := pairs["max_line_length"].Value; v == "off" {
		set("editor.max_line_length", 0, src("max_line_length"))
	} else if n, ok := num("max_line_length"); ok {
		set("editor.max_line_length", n, src("max_line_length"))
	}
	for _, e := range res.Problems {
		c.warn(e.Error(), e.Error())
	}

	if on, _, _ := c.h.Options().Get("editor.modelines", c.h.Here()); on == true {
		for name, v := range modeline(path) {
			set(name, v.v, c.rel(path)+":"+v.src+" (modeline)")
		}
	}
	return out
}

func (c *conventions) charset(v, src string, set func(string, any, string)) {
	switch v {
	case "utf-8", "utf-8-bom":
		set("editor.charset", v, src)
	default:
		c.warn("charset "+v, fmt.Sprintf("Oriel edits UTF-8 only, so %s's %s is read and written as UTF-8.", src, v))
	}
}

func (c *conventions) warn(key, text string) {
	if c.warned[key] {
		return
	}
	c.warned[key] = true
	c.h.Notify(core.Notice{ID: "conventions", Severity: core.SevWarning, Title: "File conventions", Text: text})
}

var (
	modelineSet   = regexp.MustCompile(`(?:^|[ \t])(?:vi|vim|Vim|ex):[ \t]*se(?:t)? (.*?):`)
	modelinePlain = regexp.MustCompile(`(?:^|[ \t])(?:vi|vim):[ \t]*(.*)$|[ \t]ex:[ \t]*(.*)$`)
)

// modelineOptions are the only options a modeline may set: display and indent.
var modelineOptions = map[string]bool{"tabstop": true, "shiftwidth": true, "expandtab": true}

// modeline reads the vim modelines in a file's first and last five lines (nvim's 'modelines'), for the options it
// allows; the value's src is the line number.
func modeline(path string) map[string]value {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	out := map[string]value{}
	for i, l := range lines {
		if i >= 5 && i < len(lines)-5 {
			continue
		}
		var opts []string
		if m := modelineSet.FindStringSubmatch(l); m != nil {
			opts = strings.Fields(m[1])
		} else if m := modelinePlain.FindStringSubmatch(l); m != nil {
			opts = strings.FieldsFunc(m[1]+m[2], func(r rune) bool { return r == ' ' || r == '\t' || r == ':' })
		}
		for _, o := range opts {
			name, val, hasVal := strings.Cut(o, "=")
			on := true
			if !hasVal && strings.HasPrefix(name, "no") {
				name, on = name[2:], false
			}
			long := map[string]string{"ts": "tabstop", "sw": "shiftwidth", "et": "expandtab"}[name]
			if long == "" {
				long = name
			}
			if !modelineOptions[long] {
				continue
			}
			var v any = on
			if long != "expandtab" {
				n, err := strconv.Atoi(val)
				if err != nil || n <= 0 {
					continue
				}
				v = n
			}
			out[vimreg.Name(long)] = value{v, strconv.Itoa(i + 1)}
		}
	}
	return out
}

// refresh reads every open file's conventions again and applies what changed.
func (c *conventions) refresh() {
	c.last = time.Now()
	buffers := c.buffers()
	o := c.h.Options()
	open := map[string]bool{}
	for _, name := range buffers {
		open[name] = true
		at := registry.At{Buffer: name}
		got := c.resolve(c.abs(name))
		had := c.applied[name]
		now := map[string]any{}
		for opt, v := range got {
			now[opt] = v
			if old, ok := had[opt]; ok && old == v {
				continue
			}
			if err := o.SetFrom(opt, registry.LayerFile, at, v.v, v.src); err != nil {
				c.warn(opt+v.src, fmt.Sprintf("%s: %v", v.src, err))
			}
		}
		for opt := range had {
			if _, ok := got[opt]; !ok {
				o.Unset(opt, registry.LayerFile, at)
			}
		}
		c.applied[name] = now
	}
	for name, had := range c.applied {
		if !open[name] {
			for opt := range had {
				o.Unset(opt, registry.LayerFile, registry.At{Buffer: name})
			}
			delete(c.applied, name)
		}
	}
}

// searchable leaves out of names (relative to the work dir) the files .gitattributes marks generated or vendored.
func (c *conventions) searchable(names []string) []string {
	var out []string
	for _, n := range names {
		r, rel := c.repo(c.abs(n))
		attrs, _ := r.Attributes(rel)
		if g := attrs["linguist-generated"]; g.Set || g.Text == "true" {
			continue
		}
		if v := attrs["linguist-vendored"]; v.Set || v.Text == "true" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// due reports whether it's time to look at the files again.
func (c *conventions) due() bool { return time.Since(c.last) >= conventionsEvery }

// readOnly says why a file opens read-only by its conventions: .gitattributes calls it binary.
func (c *conventions) readOnly(path string) string {
	if v, ok := c.resolve(path)["editor.binary"]; ok && v.v == true {
		return "binary (" + v.src + ")"
	}
	return ""
}

// beforeWrite applies a file's save-time conventions: trailing white space off the buffer, and its line endings,
// byte-order mark and final newline.
func (c *conventions) beforeWrite(name string, buf *text.Buffer, f *edit.File) {
	o := c.h.Options()
	at := c.h.Here()
	at.Buffer = name
	get := func(k string) any { v, _, _ := o.Get(k, at); return v }
	if get("editor.trim_trailing_whitespace") == true {
		trimTrailing(buf)
	}
	switch get("editor.end_of_line") {
	case "lf":
		f.Format.CRLF = false
	case "crlf":
		f.Format.CRLF = true
	}
	switch get("editor.charset") {
	case "utf-8":
		f.Format.BOM = false
	case "utf-8-bom":
		f.Format.BOM = true
	}
	f.Format.NoEOL = get("editor.insert_final_newline") == false
}

// trimTrailing removes white space at the ends of buf's lines, as one undo step.
func trimTrailing(buf *text.Buffer) {
	s := buf.Snapshot()
	var rs []text.Replace
	sc := bufio.NewScanner(bytes.NewReader(s.Bytes()))
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	off := 0
	for sc.Scan() {
		l := sc.Bytes()
		if t := bytes.TrimRight(l, " \t"); len(t) < len(l) {
			rs = append(rs, text.Replace{Range: text.Range{Start: off + len(t), End: off + len(l)}})
		}
		off += len(l) + 1
	}
	if len(rs) > 0 {
		buf.Apply(text.Edit{Ranges: rs, Author: text.Author{Kind: text.User}})
	}
}
