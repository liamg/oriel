package ratchet

import (
	"go/ast"
	"slices"
	"strings"
)

// GoStmts finds go statements: work started off the loop that nothing counts, so nothing can wait for it.
func GoStmts(files []File) []Hit {
	var hits []Hit
	for _, f := range files {
		ast.Inspect(f.AST, func(n ast.Node) bool {
			if g, ok := n.(*ast.GoStmt); ok {
				hits = append(hits, Hit{pos(f, g), "go"})
			}
			return true
		})
	}
	return hits
}

// Untagged are the files whose build constraint doesn't name tag.
func Untagged(files []File, tag string) []File {
	var out []File
	for _, f := range files {
		if !buildTag(f, tag) {
			out = append(out, f)
		}
	}
	return out
}

func buildTag(f File, tag string) bool {
	for _, g := range f.AST.Comments {
		if g.Pos() > f.AST.Package {
			break
		}
		for _, c := range g.List {
			if expr, ok := strings.CutPrefix(c.Text, "//go:build "); ok {
				words := strings.FieldsFunc(expr, func(r rune) bool {
					return !(r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
				})
				return slices.Contains(words, tag) && !strings.Contains(expr, "!"+tag)
			}
		}
	}
	return false
}

// AnyPkgSelectors finds pkg.name for any of names.
func AnyPkgSelectors(files []File, pkg string, names ...string) []Hit {
	var hits []Hit
	for _, n := range names {
		hits = append(hits, PkgSelectors(files, pkg, n)...)
	}
	return hits
}
