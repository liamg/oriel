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

// A set's idle parsers are its own: closing it closes them, and a parser given back after closes rather than
// staying idle. A language in no set keeps none.
func TestIdleParsersAreTheSets(t *testing.T) {
	var base *Language
	for _, l := range Builtin().Languages() {
		if l.Grammar != nil {
			base = l
			break
		}
	}
	made := func() *Language {
		return &Language{Name: base.Name, Extensions: base.Extensions, Grammar: base.Grammar, Queries: base.Queries}
	}
	l := made()
	s := NewSet(l)
	held, err := Borrow(l)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Borrow(l)
	if err != nil {
		t.Fatal(err)
	}
	p.Release()
	if n := len(s.idle.by[l]); n != 1 {
		t.Fatalf("%d idle after a release", n)
	}
	s.Close()
	if len(s.idle.by) != 0 {
		t.Errorf("a closed set keeps %d languages' parsers", len(s.idle.by))
	}
	held.Release()
	if len(s.idle.by) != 0 {
		t.Error("a parser given back to a closed set was kept")
	}
	alone := made()
	q, err := Borrow(alone)
	if err != nil {
		t.Fatal(err)
	}
	q.Release()
	if again, _ := Borrow(alone); again == q {
		t.Error("a language in no set kept a parser")
	} else {
		again.Close()
	}
}
