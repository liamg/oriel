package testrun

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// goRewrite is a subtest's name as go test reports it: white space becomes underscores and unprintable runes
// are escaped, as testing's rewrite does.
func goRewrite(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case goSpace(r):
			b.WriteByte('_')
		case !strconv.IsPrint(r):
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func goSpace(r rune) bool {
	if r < 0x2000 {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0, 0x1680:
			return true
		}
		return false
	}
	if r <= 0x200a {
		return true
	}
	switch r {
	case 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return false
}

// goNamer gives subtests their unique names as testing's matcher does: a repeated name, or an empty one, gets
// a "#NN" suffix.
type goNamer map[string]int

func (n goNamer) name(parent, sub string) string {
	base := parent + "/" + goRewrite(sub)
	for {
		c := n[base]
		n[base] = c + 1
		if c == 0 && sub != "" {
			return base
		}
		name := fmt.Sprintf("%s#%02d", base, c)
		if n[name] != 0 {
			continue
		}
		n[name] = 1
		return name
	}
}

// goRunPattern is a -run pattern that selects names: each level of the slash-separated names anchored and quoted,
// cut to the shallowest name's depth so no selected test loses subtests the pattern doesn't name.
func goRunPattern(names []string) string {
	if len(names) == 0 {
		return ""
	}
	depth := -1
	for _, n := range names {
		if d := len(strings.Split(n, "/")); depth < 0 || d < depth {
			depth = d
		}
	}
	levels := make([][]string, depth)
	seen := make([]map[string]bool, depth)
	for _, n := range names {
		for i, part := range strings.Split(n, "/")[:depth] {
			if seen[i] == nil {
				seen[i] = map[string]bool{}
			}
			if !seen[i][part] {
				seen[i][part] = true
				levels[i] = append(levels[i], regexp.QuoteMeta(part))
			}
		}
	}
	parts := make([]string, depth)
	for i, alts := range levels {
		if len(alts) == 1 {
			parts[i] = "^" + alts[0] + "$"
		} else {
			parts[i] = "^(" + strings.Join(alts, "|") + ")$"
		}
	}
	return strings.Join(parts, "/")
}
