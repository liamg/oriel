package catalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/script/api"
)

// Problem is something wrong with a plugin, where it is.
type Problem struct {
	File string // relative to the plugin; "" for the plugin as a whole
	Line int
	Text string
}

func (p Problem) String() string {
	switch {
	case p.File == "":
		return p.Text
	case p.Line == 0:
		return p.File + ": " + p.Text
	}
	return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Text)
}

// Undeclared are the statically visible oriel.* uses in the plugin's Lua (init.lua and lua/) whose capability its
// manifest doesn't declare (catalog.md, oriel plugin pack). A use is oriel.ns.fn, or the same through a local
// alias (local L = oriel). A path-taking function given a literal relative path is the plugin's own file and needs
// nothing.
func Undeclared(dir string, m plugin.Manifest) ([]Problem, error) {
	caps := apiCaps()
	declared := map[plugin.Capability]bool{}
	for _, g := range m.Permissions {
		declared[g.Cap] = true
	}
	var out []Problem
	for _, f := range luaFiles(dir) {
		src, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return nil, err
		}
		for _, u := range uses(string(src)) {
			c, ok := caps[u.name]
			if !ok {
				continue
			}
			need := plugin.Capability(c)
			if c == "target" {
				if need = targetCap(u); need == "" {
					continue
				}
			}
			if need.Always() || declared[need] {
				continue
			}
			out = append(out, Problem{File: f, Line: u.line,
				Text: fmt.Sprintf("%s needs %s, which oriel.toml doesn't declare", u.name, need)})
		}
	}
	return out, nil
}

// apiCaps is each oriel.* function's capability, from the API's reference: "oriel.process.spawn" → "target".
func apiCaps() map[string]string {
	out := map[string]string{}
	for _, fs := range api.Reference {
		for _, f := range fs {
			name, _, _ := strings.Cut(f.Call, "(")
			out[name] = f.Cap
		}
	}
	return out
}

// targetCap is what a function that checks its own target needs for this use: nothing for the plugin's own file.
func targetCap(u use) plugin.Capability {
	own := u.arg != nil && *u.arg != "" && !filepath.IsAbs(*u.arg) && !strings.HasPrefix(*u.arg, "~") &&
		!slices.Contains(strings.Split(filepath.ToSlash(*u.arg), "/"), "..")
	switch u.name {
	case "oriel.process.spawn":
		if !own {
			return plugin.CapExec
		}
	case "oriel.read", "oriel.wasm.load":
		if !own {
			return plugin.CapFSRead
		}
	case "oriel.http.request":
		return plugin.CapNet
	}
	return ""
}

// luaFiles are the plugin's Lua files, relative to dir: init.lua and everything under lua/.
func luaFiles(dir string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(dir, plugin.EntryFile)); err == nil {
		out = append(out, plugin.EntryFile)
	}
	filepath.WalkDir(filepath.Join(dir, "lua"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".lua") {
			rel, _ := filepath.Rel(dir, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out[min(1, len(out)):])
	return out
}

// use is a reference to an oriel.* function, with its first argument when that's a string literal.
type use struct {
	name string
	line int
	arg  *string
}

// uses finds the oriel.* references in Lua source, through aliases too.
func uses(src string) []use {
	toks := lex(src)
	aliases := map[string]bool{"oriel": true}
	// local L = oriel (or L = oriel): L is oriel too.
	for i := 0; i+2 < len(toks); i++ {
		if toks[i].kind == tIdent && toks[i+1].text == "=" && toks[i+2].kind == tIdent && toks[i+2].text == "oriel" &&
			(i+3 >= len(toks) || toks[i+3].text != ".") {
			aliases[toks[i].text] = true
		}
	}
	var out []use
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind != tIdent || !aliases[t.text] || (i > 0 && (toks[i-1].text == "." || toks[i-1].text == ":")) {
			continue
		}
		name := "oriel"
		j := i + 1
		for j+1 < len(toks) && toks[j].text == "." && toks[j+1].kind == tIdent {
			name += "." + toks[j+1].text
			j += 2
		}
		if name == "oriel" {
			continue
		}
		u := use{name: name, line: t.line}
		if j < len(toks) && toks[j].kind == tString {
			u.arg = &toks[j].text
		} else if j+1 < len(toks) && toks[j].text == "(" && toks[j+1].kind == tString {
			u.arg = &toks[j+1].text
		}
		out = append(out, u)
		i = j - 1
	}
	return out
}

type tokKind int

const (
	tIdent tokKind = iota
	tString
	tOther
)

type token struct {
	kind tokKind
	text string
	line int
}

// lex splits Lua into identifiers, strings (their value) and single punctuation, with comments and numbers dropped.
func lex(src string) []token {
	var out []token
	line := 1
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "--"):
			i += 2
			if n, ok := longOpen(src[i:]); ok {
				end := strings.Index(src[i+n:], longClose(src[i:i+n]))
				if end < 0 {
					end = len(src) - i - n
				}
				line += strings.Count(src[i:i+n+end], "\n")
				i += n + end + n
				continue
			}
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '"' || c == '\'':
			start := line
			var b strings.Builder
			i++
			for i < len(src) && src[i] != c && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) {
					i++
					if src[i] == '\n' {
						line++
					}
				}
				b.WriteByte(src[i])
				i++
			}
			i++
			out = append(out, token{tString, b.String(), start})
		case c == '[':
			if n, ok := longOpen(src[i:]); ok {
				end := strings.Index(src[i+n:], longClose(src[i:i+n]))
				if end < 0 {
					end = len(src) - i - n
				}
				s := src[i+n : i+n+end]
				out = append(out, token{tString, strings.TrimPrefix(s, "\n"), line})
				line += strings.Count(s, "\n")
				i += n + end + n
				continue
			}
			out = append(out, token{tOther, "[", line})
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			out = append(out, token{tIdent, src[i:j], line})
			i = j
		case c >= '0' && c <= '9':
			for i < len(src) && (src[i] == '.' || src[i] == 'x' || src[i] >= '0' && src[i] <= '9' || src[i] >= 'a' && src[i] <= 'f' || src[i] >= 'A' && src[i] <= 'F') {
				i++
			}
		default:
			if strings.HasPrefix(src[i:], "..") {
				out = append(out, token{tOther, "..", line})
				i += 2
				continue
			}
			out = append(out, token{tOther, string(c), line})
			i++
		}
	}
	return out
}

// longOpen is the length of a long bracket opening s ([[ or [==[), if one does.
func longOpen(s string) (int, bool) {
	if !strings.HasPrefix(s, "[") {
		return 0, false
	}
	n := 1
	for n < len(s) && s[n] == '=' {
		n++
	}
	return n + 1, n < len(s) && s[n] == '['
}

func longClose(open string) string {
	return "]" + strings.Repeat("=", len(open)-2) + "]"
}

func sorted(s []string) []string {
	sort.Strings(s)
	return s
}
