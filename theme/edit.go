package theme

import (
	"strings"

	"github.com/liamg/oriel/toml"
)

var semantic = map[string]bool{}

func init() {
	for _, t := range Tokens() {
		semantic[t.Name] = true // everything declared before init is theme's own
	}
}

// Section is where a name's override goes in a theme file: [palette] for "palette:x", [syntax] for a capture,
// [semantic] for theme's own tokens and [component] for the rest.
func Section(name string) []string {
	switch {
	case strings.HasPrefix(name, "palette:"):
		return []string{"palette", strings.TrimPrefix(name, "palette:")}
	case strings.HasPrefix(name, "@"):
		return []string{"syntax", name}
	case semantic[name]:
		return []string{"semantic", name}
	}
	return []string{"component", name}
}

// candidates are where name's value may already be, most specific first: under v's table, then the shared one,
// each as a quoted or a dotted key.
func candidates(v Variant, name string) [][]string {
	sec := Section(name)
	var tables [][]string
	if v != "" {
		tables = append(tables, []string{string(v), sec[0]})
	}
	tables = append(tables, sec[:1])
	var out [][]string
	for _, t := range tables {
		out = append(out, append(t[:len(t):len(t)], sec[1]))
		if sec[0] != "syntax" && strings.Contains(sec[1], ".") {
			out = append(out, append(t[:len(t):len(t)], strings.Split(sec[1], ".")...))
		}
	}
	return out
}

// Override sets name to expr in the theme file src, changing as little as it can: an existing value in place,
// else a new key under v's table (the shared one when v is ""). A style table keeps its other fields.
func Override(src []byte, v Variant, name, expr string) ([]byte, error) {
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, err
	}
	for _, p := range candidates(v, name) {
		switch cur, _ := doc.Get(p...); cur.(type) {
		case string:
			return set(doc, p, expr)
		case map[string]any:
			if Section(name)[0] == "syntax" {
				return set(doc, append(p, "fg"), expr)
			}
		}
	}
	return set(doc, candidates(v, name)[0], expr)
}

func set(doc *toml.Document, p []string, expr string) ([]byte, error) {
	if err := doc.Set(p, expr); err != nil {
		return nil, err
	}
	return doc.Bytes(), nil
}

// Reset removes name's override from src, the most specific one first, and its table once that's empty.
func Reset(src []byte, v Variant, name string) ([]byte, error) {
	doc, err := toml.Parse(src)
	if err != nil {
		return nil, err
	}
	for _, p := range candidates(v, name) {
		if _, ok := doc.Get(p...); !ok {
			continue
		}
		if err := doc.Delete(p...); err != nil {
			return nil, err
		}
		for t := p[:len(p)-1]; len(t) > 0; t = t[:len(t)-1] {
			if m, ok := doc.Get(t...); !ok || len(m.(map[string]any)) > 0 {
				break
			}
			if err := doc.Delete(t...); err != nil {
				return nil, err
			}
		}
		break
	}
	return doc.Bytes(), nil
}
