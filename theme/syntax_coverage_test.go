package theme

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var captureName = regexp.MustCompile(`@[\w.]+`)

// queryCaptures is the highlight captures a built-in language's query names, leaving out those that aren't
// highlights (syntax.Highlights drops them).
func queryCaptures(t *testing.T, lang string) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "syntax", "queries", lang, "highlights.scm"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for line := range strings.Lines(string(src)) {
		if strings.HasPrefix(strings.TrimSpace(line), ";") {
			continue
		}
		for _, c := range captureName.FindAllString(line, -1) {
			if strings.HasPrefix(c, "@_") || slices.Contains([]string{"@spell", "@nospell", "@conceal", "@none"}, c) {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

// Every capture markdown and the HTML tags in it highlight with resolves to a group oriel styles, so none is
// left in plain text.
func TestOrielStylesMarkdownAndHTML(t *testing.T) {
	for _, v := range []Variant{Dark, Light} {
		groups := resolve(t, v, oriel(t)).SyntaxGroups()
		for _, lang := range []string{"markdown", "markdown_inline", "html_tags"} {
			for _, c := range queryCaptures(t, lang) {
				styled := false
				for name := c; !styled && name != ""; {
					styled = slices.Contains(groups, name)
					name = name[:max(strings.LastIndexByte(name, '.'), 0)]
				}
				if !styled {
					t.Errorf("%s (%s): %s falls back to plain text", lang, v, c)
				}
			}
		}
	}
}
