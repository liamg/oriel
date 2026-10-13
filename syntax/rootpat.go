package syntax

import (
	"strings"
	"unicode"
)

// splitRootPatterns takes the patterns on a tree's root node (root, as "source_file") out of a query's patterns,
// given as their source text in order, as tree-sitter matches one of those against every top-level node, wherever
// the range. One that only lists siblings becomes those siblings as a group, with #has-parent? root on its first
// capture, which matches only around the range; one it can't rewrite (anchored to the first or last child,
// fields, no capture, inside a group) goes into rest, to run without the window. mainOrder and restOrder give
// each pattern of main and rest its index among patterns, so later patterns still win.
func splitRootPatterns(patterns []string, root string) (main, rest string, mainOrder, restOrder []int) {
	var m, r strings.Builder
	for i, pat := range patterns {
		if !mentions(pat, root) {
			m.WriteString(pat + "\n")
			mainOrder = append(mainOrder, i)
			continue
		}
		if body, ok := rootBody(pat, root); ok {
			if cap, ok := siblingCapture(body); ok {
				m.WriteString("(" + body + " (#has-parent? @" + cap + " " + root + "))\n")
				mainOrder = append(mainOrder, i)
				continue
			}
		}
		r.WriteString(pat + "\n")
		restOrder = append(restOrder, i)
	}
	return m.String(), r.String(), mainOrder, restOrder
}

// rootBody is what's inside an item that's exactly a pattern on root: "(source_file …)".
func rootBody(item, root string) (string, bool) {
	t := strings.TrimSpace(stripComments(item))
	if !strings.HasPrefix(t, "("+root) || !strings.HasSuffix(t, ")") {
		return "", false
	}
	rest := t[len(root)+1 : len(t)-1]
	if rest == "" || !unicode.IsSpace(rune(rest[0])) || closesEarly(t) {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// closesEarly is whether the item's first form closes before its end: "(source_file) @x" isn't one pattern body.
func closesEarly(t string) bool {
	depth := 0
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '"':
			for i++; i < len(t) && t[i] != '"'; i++ {
				if t[i] == '\\' {
					i++
				}
			}
		case '(', '[':
			depth++
		case ')', ']':
			depth--
			if depth == 0 && i != len(t)-1 {
				return true
			}
		}
	}
	return false
}

// siblingCapture is the first capture in a body that only lists sibling nodes, quantifiers, captures, anchors
// between them and predicates; false for one anchored at its start or end, or with fields.
func siblingCapture(body string) (string, bool) {
	if strings.HasPrefix(body, ".") || strings.HasSuffix(body, ".") {
		return "", false
	}
	depth, cap := 0, ""
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '"':
			for i++; i < len(body) && body[i] != '"'; i++ {
				if body[i] == '\\' {
					i++
				}
			}
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && c == '@' && cap == "":
			j := i + 1
			for j < len(body) && !unicode.IsSpace(rune(body[j])) && body[j] != ')' {
				j++
			}
			cap = body[i+1 : j]
		case depth == 0 && (c == ':' || c == '!'):
			return "", false
		}
	}
	return cap, cap != ""
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		inStr := false
		for i := 0; i < len(line); i++ {
			if line[i] == '"' && (i == 0 || line[i-1] != '\\') {
				inStr = !inStr
			}
			if line[i] == ';' && !inStr {
				line = line[:i]
				break
			}
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// mentions is whether a pattern has a node of kind root, outside strings and comments.
func mentions(pat, root string) bool {
	t := stripComments(pat)
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '"':
			for i++; i < len(t) && t[i] != '"'; i++ {
				if t[i] == '\\' {
					i++
				}
			}
		case '(':
			j := i + 1
			for j < len(t) && t[j] == ' ' {
				j++
			}
			if strings.HasPrefix(t[j:], root) {
				k := j + len(root)
				if k == len(t) || t[k] == ')' || unicode.IsSpace(rune(t[k])) {
					return true
				}
			}
		}
	}
	return false
}
