package syntax

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// A borrowed parser, given back and borrowed again for another file of its language, highlights as a new one does.
func TestBorrowedParserMatchesNew(t *testing.T) {
	byLang := map[*Language][][]byte{}
	for _, f := range fixtures() {
		if strings.HasSuffix(f, ".golden") {
			continue
		}
		l, ok := forFile(f)
		src, err := os.ReadFile(f)
		if !ok || err != nil {
			continue
		}
		byLang[l] = append(byLang[l], src, src[:len(src)/2])
	}
	for l, srcs := range byLang {
		var first *Parser
		for i, src := range srcs {
			p, err := Borrow(l)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				first = p
			} else if p != first {
				t.Errorf("%s: a parser given back wasn't borrowed again", l.Name)
			}
			p.Parse(src)
			got, _ := p.Highlights(0, len(src))
			p.Release()
			fresh, _ := NewParser(l)
			fresh.Parse(src)
			want, _ := fresh.Highlights(0, len(src))
			fresh.Close()
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s, source %d: a borrowed parser's highlights differ from a new one's", l.Name, i)
			}
		}
	}
}
