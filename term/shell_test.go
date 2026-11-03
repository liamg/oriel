package term

import (
	"reflect"
	"testing"
)

type scanned struct {
	at   int // bytes of the stream before the mark's end
	mark Mark
}

func scanAll(s *MarkScanner, chunks ...[]byte) []scanned {
	var out []scanned
	off := 0
	for _, p := range chunks {
		for len(p) > 0 {
			n, m, ok := s.Next(p)
			off += n
			if ok {
				out = append(out, scanned{off, m})
			}
			p = p[n:]
		}
	}
	return out
}

// ref:   semantic-prompts/semantic-prompts.md#Commands
// quote: "`OSC \"133;D\"` [`\";\"` _exit-code_ _options ]`\"\\007\"`"
// ref:   xterm/ctlseqs.txt OSC Ps ; Pt ST
// quote: "XTerm accepts either BEL  or ST  for terminating OSC sequences"
func TestMarkScannerSplits(t *testing.T) {
	stream := []byte("x\x1b]133;A\x07$ \x1b]133;B;k=i\x1b\\ls\r\n\x1b]0;title\x07\x1b]133;C\x07out\x1b]133;D;127;err=x\x07\x1b]133;D\x07\x1b]1330;no\x07\x1b[31m")
	want := []scanned{
		{9, Mark{Kind: 'A'}},
		{24, Mark{Kind: 'B'}},
		{46, Mark{Kind: 'C'}},
		{67, Mark{Kind: 'D', ExitCode: 127, HasExit: true}},
		{75, Mark{Kind: 'D'}},
	}
	if got := scanAll(&MarkScanner{}, stream); !reflect.DeepEqual(got, want) {
		t.Fatalf("whole stream: got %v, want %v", got, want)
	}
	for i := range len(stream) + 1 {
		for j := i; j <= len(stream); j++ {
			got := scanAll(&MarkScanner{}, stream[:i], stream[i:j], stream[j:])
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("split at %d and %d: got %v, want %v", i, j, got, want)
			}
		}
	}
}

func TestMarkScannerPending(t *testing.T) {
	for head, rest := range map[string]string{"\x1b]13": "3;A\x07", "\x1b]133;D;": "2\x07", "\x1b]133;D;4\x1b": "\\"} {
		var a MarkScanner
		scanAll(&a, []byte(head))
		var b MarkScanner
		b.Next(a.Pending())
		ga, gb := scanAll(&a, []byte(rest)), scanAll(&b, []byte(rest))
		if len(ga) != 1 || !reflect.DeepEqual(ga, gb) {
			t.Errorf("after %q: original %v, restored %v", head, ga, gb)
		}
	}
}

type fakeAnchor struct {
	p     Point
	gone  bool
	freed *int
}

func (a *fakeAnchor) Point() (Point, bool) { return a.p, !a.gone }
func (a *fakeAnchor) Free()                { *a.freed++ }

func TestCommandLog(t *testing.T) {
	freed := 0
	at := func(x, y int) *fakeAnchor { return &fakeAnchor{p: Point{x, y}, freed: &freed} }
	l := CommandLog{Max: 2}
	first := at(0, 0)
	l.Mark(Mark{Kind: 'A'}, first)
	l.Mark(Mark{Kind: 'B'}, at(2, 0))
	l.Mark(Mark{Kind: 'C'}, at(0, 1))
	l.Mark(Mark{Kind: 'D', ExitCode: 1, HasExit: true}, at(0, 2))
	l.Mark(Mark{Kind: 'A'}, at(0, 2))
	l.Mark(Mark{Kind: 'Z'}, at(0, 9)) // unknown marks are dropped
	if freed != 1 {
		t.Errorf("freed %d anchors, want the unknown mark's", freed)
	}
	none := Point{-1, -1}
	want := []Command{
		{Prompt: Point{0, 0}, Input: Point{2, 0}, Output: Point{0, 1}, End: Point{0, 2}, Finished: true, ExitCode: 1, HasExit: true},
		{Prompt: Point{0, 2}, Input: none, Output: none, End: none},
	}
	if got := l.Commands(); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	l.Mark(Mark{Kind: 'A'}, at(0, 3))
	if got := l.Commands(); len(got) != 2 || got[0].Prompt != (Point{0, 2}) || freed != 5 {
		t.Errorf("past Max the oldest command goes, its anchors freed: %+v, freed %d", got, freed)
	}
	saved := l.Save()
	var m CommandLog
	m.Load(saved, func(p Point) Anchor { return at(p.X, p.Y) })
	if !reflect.DeepEqual(m.Commands(), l.Commands()) {
		t.Errorf("loaded %+v, saved %+v", m.Commands(), l.Commands())
	}
	for _, c := range l.cmds {
		for _, a := range c.marks {
			if a != nil {
				a.(*fakeAnchor).gone = true
			}
		}
	}
	if got := l.Commands(); len(got) != 0 {
		t.Errorf("commands whose marks are all gone stay: %+v", got)
	}
}

// ref:   ghostty/osc/7.mdx#Change Working Directory (OSC 7)
// quote: "The URI should always have a scheme of `file://` and point to an existent folder."
func TestCwdFromURL(t *testing.T) {
	for in, want := range map[string]string{
		"file://host/home/me/a%20b": "/home/me/a b",
		"file:///tmp":               "/tmp",
		"/bare/path":                "/bare/path",
		"":                          "",
	} {
		if got := CwdFromURL(in); got != want {
			t.Errorf("CwdFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
