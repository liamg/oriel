package syntax

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

var update = flag.Bool("update", false, "rewrite the golden highlight snapshots")

// snapshot renders spans under each line they cover, one caret line per span:
//
//	3 | package shapes
//	  | ^^^^^^^ keyword.import
func snapshot(src []byte, spans []Span) string {
	var b strings.Builder
	lines := bytes.SplitAfter(src, []byte("\n"))
	off := 0
	si := 0
	for n, line := range lines {
		if len(line) == 0 {
			continue
		}
		end := off + len(line)
		fmt.Fprintf(&b, "%4d | %s", n+1, strings.TrimSuffix(string(line), "\n"))
		b.WriteByte('\n')
		for i := si; i < len(spans) && spans[i].Range.Start < end; i++ {
			s, e := max(spans[i].Range.Start, off), min(spans[i].Range.End, end)
			body := bytes.TrimSuffix(line, []byte("\n"))
			if s >= e || s-off >= len(body) {
				continue
			}
			e = min(e, off+len(body))
			pad := utf8.RuneCount(line[:s-off])
			width := max(utf8.RuneCount(line[s-off:e-off]), 1)
			fmt.Fprintf(&b, "     | %s%s %s\n", strings.Repeat(" ", pad), strings.Repeat("^", width), spans[i].Capture)
		}
		for si < len(spans) && spans[si].Range.End <= end {
			si++
		}
		off = end
	}
	return b.String()
}

// TestHighlightSnapshots checks highlighting of a fixture per built-in language against its golden snapshot.
// Regenerate with -update and review the diff.
func TestHighlightSnapshots(t *testing.T) {
	files, _ := filepath.Glob("testdata/highlight/fixture.*")
	if len(files) < 6 {
		t.Fatalf("want a fixture for Go, TypeScript, Python, Rust, Lua and Markdown; found %v", files)
	}
	for _, f := range files {
		if strings.HasSuffix(f, ".golden") {
			continue
		}
		t.Run(filepath.Ext(f)[1:], func(t *testing.T) {
			l, ok := ForPath(f)
			if !ok {
				t.Fatalf("no language for %s", f)
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			p, err := NewParser(l)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			p.Parse(src)
			if p.HasError() {
				t.Errorf("%s doesn't parse cleanly", f)
			}
			spans, err := p.Highlights(0, len(src))
			if err != nil {
				t.Fatal(err)
			}
			got := snapshot(src, spans)
			golden := f + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("highlights differ from %s:\n%s", golden, firstDiff(string(want), got))
			}
		})
	}
}

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\nwant %q\n got %q", i+1, a, b)
		}
	}
	return ""
}

// TestIncrementalMatchesFullParse edits fixtures at random and checks that the incrementally updated tree
// highlights exactly as a fresh parse of the same text does.
func TestIncrementalMatchesFullParse(t *testing.T) {
	files, _ := filepath.Glob("testdata/highlight/fixture.*")
	r := rand.New(rand.NewPCG(1, 2))
	inserts := []string{"x", " ", "\n", "(", ")", "\"", "{", "}", "//", "é", "0"}
	for _, f := range files {
		if strings.HasSuffix(f, ".golden") {
			continue
		}
		l, _ := ForPath(f)
		src, _ := os.ReadFile(f)
		inc, _ := NewParser(l)
		full, _ := NewParser(l)
		buf := text.New(src, text.Author{})
		inc.ParseSnapshot(buf.Snapshot())
		for step := range 40 {
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
			inc.Update(ch)
			full.Parse(ch.After.Bytes())
			a, err1 := inc.Highlights(0, ch.After.Len())
			b, err2 := full.Highlights(0, ch.After.Len())
			if err1 != nil || err2 != nil {
				t.Fatal(err1, err2)
			}
			if fmt.Sprint(a) != fmt.Sprint(b) {
				t.Fatalf("%s step %d: incremental and full highlights differ", f, step)
			}
		}
		inc.Close()
		full.Close()
	}
}

// TestIncrementalReparseUnderOneMillisecond is the ticket's budget: typing a character into an identifier
// in a 600-line Go file reparses in under 1 ms (the median of 50 edits). Bigger files parse off the
// keystroke path, in a Document (TestDocumentNeverBlocksTyping).
func TestIncrementalReparseUnderOneMillisecond(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("timing budget: not under -short or -race")
	}
	p, buf := bigGo(t, 600)
	defer p.Close()
	lines := buf.Snapshot().Lines()
	var times []time.Duration
	for i := range 50 {
		ch := typeInIdentifier(t, buf, lines/4+i*(lines/100))
		start := time.Now()
		p.Update(ch)
		times = append(times, time.Since(start))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	median := times[len(times)/2]
	t.Logf("%d lines: median reparse %v, max %v", lines, median, times[len(times)-1])
	if median >= time.Millisecond {
		t.Errorf("median incremental reparse %v, want under 1ms", median)
	}
}

// bigGo builds a Go file of at least n lines from the fixture, parsed.
func bigGo(t *testing.T, n int) (*Parser, *text.Buffer) {
	t.Helper()
	src, _ := os.ReadFile("testdata/highlight/fixture.go")
	body := src[bytes.Index(src, []byte("const Pi")):]
	var big bytes.Buffer
	big.Write(src)
	for i := 0; strings.Count(big.String(), "\n") < n; i++ {
		big.Write(bytes.ReplaceAll(body, []byte("Total"), []byte(fmt.Sprintf("Total%d", i))))
	}
	l, _ := Lookup("go")
	p, err := NewParser(l)
	if err != nil {
		t.Fatal(err)
	}
	buf := text.New(big.Bytes(), text.Author{})
	p.ParseSnapshot(buf.Snapshot())
	return p, buf
}

// typeInIdentifier types an "x" into the first "total" at or after line.
func typeInIdentifier(t *testing.T, buf *text.Buffer, line int) text.Change {
	t.Helper()
	s := buf.Snapshot()
	ls := s.LineStart(line)
	at := ls + bytes.Index(s.Slice(text.Range{Start: ls, End: s.Len()}), []byte("total")) + 2
	ch, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: at, End: at}, Text: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// TestDocumentNeverBlocksTyping types fast into a 2,000-line file through a Document: handing it each change
// returns at once, highlights read meanwhile line up with the text, and once it catches up they equal a
// fresh parse.
func TestDocumentNeverBlocksTyping(t *testing.T) {
	p, buf := bigGo(t, 2000)
	p.Close()
	l, _ := Lookup("go")
	d, err := NewDocument(l, buf.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	lines := buf.Snapshot().Lines()
	var slowest time.Duration
	for i := range 100 {
		ch := typeInIdentifier(t, buf, lines/4+i*(lines/200))
		start := time.Now()
		d.Change(ch)
		slowest = max(slowest, time.Since(start))
		spans, _ := d.Highlights(0, ch.After.Len())
		for _, s := range spans {
			if s.Range.End > ch.After.Len() {
				t.Fatalf("a span runs past the end of the text")
			}
		}
	}
	if !raceEnabled && slowest > 200*time.Microsecond {
		t.Errorf("handing a change to the document took %v; it must not wait on a parse", slowest)
	}
	d.Wait()
	final := buf.Snapshot()
	got, _ := d.Highlights(0, final.Len())
	fresh, _ := NewParser(l)
	defer fresh.Close()
	fresh.Parse(final.Bytes())
	want, _ := fresh.Highlights(0, final.Len())
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("highlights after catching up differ from a fresh parse")
	}
}

func TestShiftSpans(t *testing.T) {
	buf := text.New([]byte("foo bar baz"), text.Author{})
	spans := []Span{{text.Range{Start: 0, End: 3}, "a"}, {text.Range{Start: 4, End: 7}, "b"}, {text.Range{Start: 8, End: 11}, "c"}}
	ch, _ := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: 5, End: 6}, Text: []byte("XYZ")}}})
	got := shiftSpans(spans, []text.Change{ch})
	want := []Span{{text.Range{Start: 0, End: 3}, "a"}, {text.Range{Start: 4, End: 9}, "b"}, {text.Range{Start: 10, End: 13}, "c"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("shiftSpans = %v, want %v", got, want)
	}
}

// Highlighting a file a chunk at a time gives what highlighting it whole does.
func TestChunkedHighlightsMatchWhole(t *testing.T) {
	for _, name := range []string{"go", "lua", "markdown", "python", "rust", "typescript"} {
		l, ok := Lookup(name)
		if !ok {
			continue
		}
		files, _ := filepath.Glob("testdata/highlight/*." + map[string]string{"go": "go", "lua": "lua", "markdown": "md", "python": "py", "rust": "rs", "typescript": "ts"}[name])
		for _, f := range files {
			src, _ := os.ReadFile(f)
			src = bytes.Repeat(src, 1+2*highlightChunk/max(len(src), 1))
			p, err := NewParser(l)
			if err != nil {
				t.Fatal(err)
			}
			p.Parse(src)
			whole, err := p.Highlights(0, len(src))
			if err != nil {
				t.Fatal(err)
			}
			chunked, err := p.highlightsInChunks(len(src))
			if err != nil {
				t.Fatal(err)
			}
			p.Close()
			if a, b := spanRuns(src, whole), spanRuns(src, chunked); a != b {
				t.Errorf("%s: chunked highlights differ from whole", f)
			}
		}
	}
}

// spanRuns is the capture at every byte, so adjacent spans of one capture compare equal to one span.
func spanRuns(src []byte, spans []Span) string {
	caps := make([]string, len(src))
	for _, s := range spans {
		for i := s.Range.Start; i < s.Range.End && i < len(src); i++ {
			caps[i] = s.Capture
		}
	}
	return strings.Join(caps, "\x00")
}
