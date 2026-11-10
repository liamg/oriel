package syntax

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/text"
)

// A Document re-highlights only what an edit changed, and ends up highlighting as a fresh parse does.
func TestDocumentRehighlightsWhatChanged(t *testing.T) {
	for _, f := range fixtures() {
		l, _ := forFile(f)
		if l == nil {
			continue
		}
		src := mustRead(t, f)
		buf := text.New(src, text.Author{})
		d, err := NewDocument(l, buf.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		h := fnv.New64a()
		h.Write([]byte(f))
		r := rand.New(rand.NewPCG(2, h.Sum64()))
		full, _ := NewParser(l)
		inserts := []string{"x", " ", "\n", "(", ")", "\"", "{", "}", "//", "0"}
		for step := range 25 {
			s := buf.Snapshot()
			from := r.IntN(s.Len() + 1)
			for !s.Boundary(from) {
				from--
			}
			to := min(s.Len(), from+r.IntN(4))
			for !s.Boundary(to) {
				to++
			}
			ch, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: from, End: to}, Text: []byte(inserts[r.IntN(len(inserts))])}}})
			if err != nil {
				t.Fatal(err)
			}
			d.Change(ch)
			d.Wait()
			full.Parse(ch.After.Bytes())
			if full.HasError() {
				continue // recovery from errors can differ between a reparse and a fresh parse
			}
			want, _ := full.highlightsInChunks(ch.After.Len())
			got, _ := d.Highlights(0, ch.After.Len())
			if fmt.Sprint(merged(got)) != fmt.Sprint(merged(want)) {
				t.Fatalf("%s step %d: the document's highlights differ from a fresh parse's", f, step)
			}
		}
		d.Close()
		full.Close()
	}

	// One character typed into a 10,000-line file queries a line or so, not the file.
	l, _ := Builtin().Language("go")
	buf := text.New(manyDecls(), text.Author{})
	d, _ := NewDocument(l, buf.Snapshot())
	defer d.Close()
	off := buf.Snapshot().LineStart(5001) + 8
	ch, _ := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: off, End: off}, Text: []byte("x")}}})
	before := d.p.queried
	d.Change(ch)
	d.Wait()
	if n := d.p.queried - before; n > 4<<10 {
		t.Errorf("a one-character edit queried %d bytes", n)
	}
}

// merged joins adjacent spans of one capture, so chunking or splicing differently compares equal.
func merged(spans []Span) []Span {
	var out []Span
	for _, s := range spans {
		if k := len(out) - 1; k >= 0 && out[k].Capture == s.Capture && out[k].Range.End == s.Range.Start {
			out[k].Range.End = s.Range.End
			continue
		}
		out = append(out, s)
	}
	return out
}

func mustRead(t *testing.T, f string) []byte {
	t.Helper()
	b, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// manyDecls is a 10,000-line Go file of many top-level declarations, the shape that made each capture's parent
// expensive to find.
func manyDecls() []byte {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"fmt\"\n\n")
	for i := 0; strings.Count(b.String(), "\n") < 9990; i++ {
		fmt.Fprintf(&b, "// f%d prints its number.\nfunc f%d(n int) string {\n\treturn fmt.Sprintf(\"%%d:%%d\", n, %d)\n}\n\n", i, i, i)
	}
	return []byte(b.String())
}
