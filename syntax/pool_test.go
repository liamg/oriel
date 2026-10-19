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

// Languages made again, as a language set is when a plugin adds one, don't keep idle parsers without end: past
// maxIdleLangs, the language released longest ago closes its.
func TestIdleParsersAreBounded(t *testing.T) {
	var base *Language
	for _, l := range Builtin().Languages() {
		if l.Grammar != nil {
			base = l
			break
		}
	}
	var made []*Language
	for range maxIdleLangs + 5 {
		l := &Language{Name: base.Name, Extensions: base.Extensions, Grammar: base.Grammar, Queries: base.Queries}
		made = append(made, l)
		p, err := Borrow(l)
		if err != nil {
			t.Fatal(err)
		}
		p.Release()
	}
	idle.Lock()
	defer idle.Unlock()
	if len(idle.order) > maxIdleLangs || len(idle.by) > maxIdleLangs {
		t.Errorf("%d languages keep idle parsers, %d in order", len(idle.by), len(idle.order))
	}
	if _, ok := idle.by[made[0]]; ok {
		t.Error("the language released longest ago still keeps its parsers")
	}
	if _, ok := idle.by[made[len(made)-1]]; !ok {
		t.Error("the language released last lost its parser")
	}
}
