package syntax

import (
	"testing"

	"github.com/liamg/oriel/text"
)

// bigDoc is a document with n spans of 5 bytes, 10 apart, as a large file's highlights.
func bigDoc(n int) *Document {
	d := &Document{}
	for i := range n {
		d.spans = append(d.spans, Span{Range: text.Range{Start: i * 10, End: i*10 + 5}, Capture: "keyword"})
	}
	return d
}

func TestDocumentHighlightsWindow(t *testing.T) {
	d := bigDoc(1000)
	got, _ := d.Highlights(23, 50)
	if len(got) != 3 || got[0].Range.Start != 20 || got[2].Range.Start != 40 {
		t.Fatalf("spans in [23, 50): %+v", got)
	}
	if got, _ := d.Highlights(5, 10); len(got) != 0 {
		t.Fatalf("spans in a gap: %+v", got)
	}
}

func BenchmarkDocumentHighlightsVisible(b *testing.B) {
	d := bigDoc(200_000) // about a 50k-line file
	mid := 1_000_000
	b.ReportAllocs()
	for b.Loop() {
		d.Highlights(mid, mid+4000)
	}
}
