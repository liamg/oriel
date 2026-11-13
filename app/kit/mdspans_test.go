package kit_test

import (
	"html"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/ui/widget"
)

// styled is text one rune at a time, each with how it's set: i(talic), b(old), s(truck), c(ode).
type styled struct {
	text  []rune
	style []string
}

func (s *styled) add(text, style string) {
	for _, r := range text {
		s.text, s.style = append(s.text, r), append(s.style, style)
	}
}

func (s styled) String() string {
	var b strings.Builder
	last := "-"
	for i, r := range s.text {
		if s.style[i] != last {
			b.WriteString("[" + s.style[i] + "]")
			last = s.style[i]
		}
		b.WriteRune(r)
	}
	return b.String()
}

func flags(i, b, s, c bool) string {
	out := ""
	for _, f := range []struct {
		on bool
		c  string
	}{{i, "i"}, {b, "b"}, {s, "s"}, {c, "c"}} {
		if f.on {
			out += f.c
		}
	}
	return out
}

var tag = regexp.MustCompile(`</?(em|strong|del|code)>|[^<]+`)

// fromHTML reads a paragraph of the spec's expected output.
func fromHTML(h string) styled {
	var out styled
	depth := map[string]int{}
	for _, m := range tag.FindAllStringSubmatch(h, -1) {
		if m[1] != "" {
			if strings.HasPrefix(m[0], "</") {
				depth[m[1]]--
			} else {
				depth[m[1]]++
			}
			continue
		}
		out.add(html.UnescapeString(m[0]), flags(depth["em"] > 0, depth["strong"] > 0, depth["del"] > 0, depth["code"] > 0))
	}
	return out
}

func fromSpans(sp []widget.Span) styled {
	var out styled
	for _, s := range sp {
		out.add(s.Text, flags(s.Italic, s.Bold, s.Strike, s.Code))
	}
	return out
}

var example = regexp.MustCompile("(?s)```````````````````````````````` example[^\n]*\n(.*?)\n\\.\n(.*?)````````````````````````````````")
var plainPara = regexp.MustCompile(`^<p>(?:[^<]|</?(?:em|strong|del|code)>)*</p>\n$`)

// Each example of the spec's emphasis and strikethrough sections that's one line of text, code, emphasis and strong
// emphasis (no links, escapes, HTML or images, which MDSpans leaves as they are), read as the spec reads it.
//
// ref:   gfm/spec.md#Emphasis and strong emphasis
// quote: "A single `*` character [can open emphasis](@) iff (if and only if) it is part of a [left-flanking delimiter run]."
// ref:   gfm/spec.md#Strikethrough (extension)
// quote: "Strikethrough text is any text wrapped in two tildes (`~`)."
func TestMDSpansReadEmphasisAsTheSpecDoes(t *testing.T) {
	b, err := os.ReadFile("../../spec/gfm/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(b)
	start := strings.Index(spec, "\n## Emphasis and strong emphasis\n")
	end := strings.Index(spec[start:], "\n## Links\n")
	strike := strings.Index(spec, "\n## Strikethrough (extension)\n")
	strikeEnd := strike + strings.Index(spec[strike+1:], "\n## ")
	n := 0
	for _, sec := range []string{spec[start : start+end], spec[strike : strikeEnd+1]} {
		for _, m := range example.FindAllStringSubmatch(sec, -1) {
			md, want := strings.ReplaceAll(m[1], "→", "\t"), m[2]
			if strings.ContainsAny(md, "\n\\<[!") || !plainPara.MatchString(want) {
				continue
			}
			n++
			got, exp := fromSpans(kit.MDSpans(md)), fromHTML(strings.TrimSuffix(strings.TrimPrefix(want, "<p>"), "</p>\n"))
			if got.String() != exp.String() {
				t.Errorf("%q:\n got  %s\n want %s", md, got, exp)
			}
		}
	}
	if n < 80 {
		t.Errorf("checked %d examples", n)
	}
	t.Logf("%d of the spec's examples", n)
}
