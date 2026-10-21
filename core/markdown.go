package core

import (
	"regexp"
	"strings"
)

var (
	mdImage   = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdComment = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)
	mdTag     = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(\s[^<>]*)?/?>`)
	mdInline  = strings.NewReplacer("`", "", "**", "", "__", "", "~~", "")
)

// PlainMarkdown is markdown as plain text: images, HTML comments and tags go, and inline markup (`code`, **bold**,
// [links](url)) keeps only its text.
func PlainMarkdown(s string) string {
	s = mdComment.ReplaceAllString(s, "")
	s = mdImage.ReplaceAllString(s, "")
	s = mdTag.ReplaceAllString(s, "")
	s = mdInline.Replace(s)
	for {
		i := strings.Index(s, "](")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], ")")
		open := strings.LastIndex(s[:i], "[")
		if j < 0 || open < 0 {
			break
		}
		s = s[:open] + s[open+1:i] + s[i+j+1:]
	}
	return s
}

// Summary is markdown's first line of plain text, for places that show it unrendered: a title, a list's preview.
func Summary(s string) string {
	for line := range strings.Lines(PlainMarkdown(s)) {
		if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>")); line != "" {
			return line
		}
	}
	return ""
}
