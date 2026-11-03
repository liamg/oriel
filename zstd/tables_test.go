package zstd

import (
	"bytes"
	"encoding/binary"
	"strconv"
	"strings"
	"testing"
)

// specTable reads the rows of the table under the line starting with title in a vendored spec: each row's cells as
// numbers, for rows whose cells all are.
func specTable(t *testing.T, path, title string) [][]int {
	t.Helper()
	text := string(read(t, path))
	i := strings.Index(text, title)
	if i < 0 {
		t.Fatalf("no %q in %s", title, path)
	}
	var rows [][]int
	started := false
	for line := range strings.Lines(text[i+len(title):]) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			if started && line != "" && !strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "RFC") &&
				!strings.HasPrefix(line, "Collet") && line != "\f" {
				break
			}
			continue
		}
		var row []int
		for c := range strings.SplitSeq(strings.Trim(line, "|"), "|") {
			n, err := strconv.Atoi(strings.TrimSpace(c))
			if err != nil {
				row = nil
				break
			}
			row = append(row, n)
		}
		if row != nil {
			rows = append(rows, row)
			started = true
		}
	}
	return rows
}

// The predefined tables are those of the RFC's Appendix A.
//
// ref:   zstd/rfc8878.txt
// quote: "The tables here can be used as examples to crosscheck that an implementation has built its decoding tables correctly."
func TestPredefinedTables(t *testing.T) {
	for kind, title := range []string{"A.1.  Literals Length Code Table", "A.3.  Offset Code Table", "A.2.  Match Length Code Table"} {
		rows := specTable(t, "../spec/zstd/rfc8878.txt", "\n"+title)
		want := map[int][]int{}
		for _, r := range rows {
			if len(r) == 4 {
				want[r[0]] = r[1:] // the table's first row, all zeros, is overwritten by state 0's
			}
		}
		size := 1 << defaultLog[kind]
		if len(want) != size {
			t.Fatalf("%s: %d states in the RFC, want %d", title, len(want), size)
		}
		var dt [64]fseEntry
		if err := buildFSE(defaultDist[kind], defaultLog[kind], dt[:size]); err != nil {
			t.Fatal(err)
		}
		for u := range size {
			e := dt[u]
			if w := want[u]; int(e.sym) != w[0] || int(e.nb) != w[1] || int(e.next) != w[2] {
				t.Errorf("%s state %d: symbol %d, bits %d, base %d; want %v", title, u, e.sym, e.nb, e.next, w)
			}
			p := predefined[kind].e[u]
			if c := code(kind, e.sym); p.base != c.base || p.extra != c.extra || p.nb != e.nb || p.next != e.next {
				t.Errorf("%s state %d: predefined entry %+v", title, u, p)
			}
		}
	}
}

// The repeat offsets move as the format's table walks them.
//
// ref:   zstd/facebook-zstd/doc/zstd_compression_format.md#Offset updates rules
// quote: "| 3 | 0 | 2221 | 2222 | 1111 | special case : insert `repeat1 - 1` |"
// ref:   zstd/rfc8878.txt
// quote: "For the first block, the starting offset history is populated with the following values: Repeated_Offset1 (1), Repeated_Offset2 (4), and Repeated_Offset3 (8)"
func TestRepeatOffsets(t *testing.T) {
	text := string(read(t, "../spec/zstd/facebook-zstd/doc/zstd_compression_format.md"))
	text = text[strings.Index(text, "###### Offset updates rules"):]
	var d decoder
	d.reset(1 << 20)
	rep := d.rep
	n := 0
	for line := range strings.Lines(text) {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 6 {
			continue
		}
		var v [5]int
		ok := true
		for i := range 5 {
			s := strings.TrimSpace(cells[i])
			if s == "" && i < 2 {
				v[i] = -1
				continue
			}
			x, err := strconv.Atoi(s)
			if err != nil {
				ok = false
				break
			}
			v[i] = x
		}
		if !ok {
			continue
		}
		if v[0] < 0 {
			if [3]int(v[2:]) != rep {
				t.Fatalf("starting values %v, want %v", rep, v[2:])
			}
			continue
		}
		repeat(&rep, v[0], v[1])
		if [3]int(v[2:]) != rep {
			t.Errorf("after %d with %d literals: %v, want %v", v[0], v[1], rep, v[2:])
		}
		n++
	}
	if n != 9 {
		t.Fatalf("read %d rows of the table", n)
	}
}

// bitWriter writes a stream for a backward reader: fields in the reverse of the order they're read, then the end mark.
type bitWriter struct {
	out  []byte
	acc  uint64
	nacc uint
}

func (w *bitWriter) put(v uint32, n uint8) {
	w.acc |= uint64(v) << w.nacc
	w.nacc += uint(n)
	for w.nacc >= 8 {
		w.out = append(w.out, byte(w.acc))
		w.acc >>= 8
		w.nacc -= 8
	}
}

func (w *bitWriter) close() []byte {
	w.put(1, 1)
	if w.nacc > 0 {
		w.out = append(w.out, byte(w.acc))
	}
	return w.out
}

// field is a value of n bits.
type field struct {
	v uint32
	n uint8
}

func backwardStream(read ...field) []byte {
	var w bitWriter
	for i := len(read) - 1; i >= 0; i-- {
		w.put(read[i].v, read[i].n)
	}
	return w.close()
}

// A compressed block of two sequences coded with the predefined tables, its states found in the RFC's Appendix A:
// "abc" from the literals, a match of 6 at offset 3, then a repeat offset with no literals.
//
// ref:   zstd/rfc8878.txt
// quote: "It starts with Literals_Length_State, followed by Offset_State, and finally Match_Length_State."
// ref:   zstd/rfc8878.txt
// quote: "Decoding starts by reading the Number_of_Bits required to decode offset. It does the same for Match_Length and then for Literals_Length."
// ref:   zstd/rfc8878.txt
// quote: "Literals_Length_State is updated, followed by Match_Length_State, and then Offset_State."
func TestPredefinedSequences(t *testing.T) {
	type state struct{ sym, nb, base int }
	tables := map[int]map[int]state{}
	for kind, title := range []string{"A.1.  Literals Length Code Table", "A.3.  Offset Code Table", "A.2.  Match Length Code Table"} {
		tables[kind] = map[int]state{}
		for _, r := range specTable(t, "../spec/zstd/rfc8878.txt", "\n"+title) {
			if len(r) == 4 {
				tables[kind][r[0]] = state{r[1], r[2], r[3]}
			}
		}
	}
	// first is a state decoding sym; then, one reachable from it decoding next, and the bits that reach it.
	pick := func(kind, sym, next int) (int, field, int) {
		for u := range len(tables[kind]) {
			s := tables[kind][u]
			if s.sym != sym {
				continue
			}
			for v := s.base; v < s.base+1<<s.nb; v++ {
				if tables[kind][v].sym == next {
					return u, field{uint32(v - s.base), uint8(s.nb)}, v
				}
			}
		}
		t.Fatalf("no states for %d then %d", sym, next)
		return 0, field{}, 0
	}
	// Literals lengths 3 then 0 (codes 3, 0); offset values 6 then 1 (codes 2, 0); match lengths 6 then 4 (codes 3, 1).
	ll, llUp, _ := pick(litLen, 3, 0)
	of, ofUp, _ := pick(offCode, 2, 0)
	ml, mlUp, _ := pick(matchLen, 3, 1)
	bits := backwardStream(
		field{uint32(ll), 6}, field{uint32(of), 5}, field{uint32(ml), 6},
		field{6 - 4, 2}, field{0, 0}, field{0, 0}, // offset 6 is 1<<2 and 2 more
		llUp, mlUp, ofUp,
		field{0, 0}, field{0, 0}, field{0, 0},
	)
	blk := append([]byte{3 << 3}, "abc"...)
	blk = append(blk, 2, 0x00)
	blk = append(blk, bits...)
	want := []byte("abcabcabccccc")
	src := frame{0x20, []byte{byte(len(want))}, [][]byte{block(true, blockCompressed, len(blk), blk)}}.bytes(want)
	got, err := both(t, src)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %q, %v", got, err)
	}

	// The block again, repeating the first's tables: its first match is offset value 6 again, its second the repeat
	// offset 2 back, 1 as the first block left them (1, 3, 4).
	rep := append([]byte{3 << 3}, "abc"...)
	rep = append(rep, 2, 0xFC)
	rep = append(rep, bits...)
	got, err = both(t, frame{0x00, []byte{0x00}, [][]byte{block(false, blockCompressed, len(blk), blk), block(true, blockCompressed, len(rep), rep)}}.bytes(nil))
	if err != nil || string(got) != string(want)+string(want) {
		t.Fatalf("got %q, %v", got, err)
	}

	// Repeat mode with no table before it, and reserved mode bits, are refused.
	bad := append([]byte{3 << 3}, "abc"...)
	bad = append(bad, 2, 0xC0)
	bad = append(bad, bits...)
	if _, err := both(t, frame{0x00, []byte{0x00}, [][]byte{block(true, blockCompressed, len(bad), bad)}}.bytes(nil)); err == nil {
		t.Error("repeat mode in the first block decoded")
	}
	bad[len("abc")+2] = 0x01
	if _, err := both(t, frame{0x00, []byte{0x00}, [][]byte{block(true, blockCompressed, len(bad), bad)}}.bytes(nil)); err == nil {
		t.Error("reserved mode bits decoded")
	}
}

// Literals coded with the RFC's example Huffman tree, in one stream and in four, then again with the tree reused.
//
// ref:   zstd/rfc8878.txt
// quote: "Symbols are sorted by Weight. Within the same Weight, symbols keep natural sequential order. Symbols with a Weight of zero are removed. Then, starting from the lowest Weight, prefix codes are distributed in sequential order."
// ref:   zstd/rfc8878.txt
// quote: "Weight[0] = (Byte[0] >> 4)"
// ref:   zstd/rfc8878.txt
// quote: "Treeless_Literals_Block: This is a Huffman-compressed block, using the Huffman tree from the previous Compressed_Literals_Block"
func TestHuffmanLiterals(t *testing.T) {
	// Table 24's weights for literals 0-4, literal 5's implied: Table 25's codes.
	tree := []byte{127 + 5, 0x43, 0x20, 0x10}
	codes := map[byte]field{0: {1, 1}, 1: {1, 2}, 2: {1, 3}, 4: {0, 4}, 5: {1, 4}}
	var h huffTable
	if n, err := h.read(tree); err != nil || n != len(tree) || h.bits != 4 {
		t.Fatalf("read %d bytes, max bits %d, %v", n, h.bits, err)
	}
	for sym, c := range codes {
		for low := range 1 << (4 - c.n) {
			e := h.dt[int(c.v)<<(4-c.n)|low]
			if byte(e>>8) != sym || uint8(e) != c.n {
				t.Errorf("code %0*b: symbol %d of %d bits, want %d", c.n, c.v, e>>8, uint8(e), sym)
			}
		}
	}
	stream := func(lits []byte) []byte {
		var f []field
		for _, l := range lits {
			f = append(f, codes[l])
		}
		return backwardStream(f...)
	}
	header := func(typ, format, regen, comp int) []byte {
		v := typ | format<<2 | regen<<4 | comp<<14
		return []byte{byte(v), byte(v >> 8), byte(v >> 16)}
	}
	one := []byte{0, 1, 4, 5, 2, 2, 0}
	s := stream(one)
	b1 := append(header(2, 0, len(one), len(tree)+len(s)), tree...)
	b1 = append(append(b1, s...), 0)

	four := []byte{5, 4, 1, 0, 0, 0, 2, 1, 4, 4}
	var streams [][]byte
	for i := range 4 {
		streams = append(streams, stream(four[i*3:min(i*3+3, len(four))]))
	}
	jump := []byte{}
	for _, st := range streams[:3] {
		jump = binary.LittleEndian.AppendUint16(jump, uint16(len(st)))
	}
	body := append([]byte{}, jump...)
	for _, st := range streams {
		body = append(body, st...)
	}
	b2 := append(header(3, 1, len(four), len(body)), body...)
	b2 = append(b2, 0)

	want := append(append([]byte{}, one...), four...)
	src := frame{0x00, []byte{0x00}, [][]byte{block(false, blockCompressed, len(b1), b1), block(true, blockCompressed, len(b2), b2)}}.bytes(nil)
	got, err := both(t, src)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %v, %v; want %v", got, err, want)
	}
	if _, err := both(t, frame{0x00, []byte{0x00}, [][]byte{block(true, blockCompressed, len(b2), b2)}}.bytes(nil)); err == nil {
		t.Error("treeless literals with no tree before them decoded")
	}
}

// XXH64 against the reference implementation's values (testdata/xxh64.txt), whole and in pieces.
func TestXXH64(t *testing.T) {
	var buf [4096]byte
	for i := range buf {
		buf[i] = byte(i*31 + 7)
	}
	n := 0
	for line := range strings.Lines(string(read(t, "testdata/xxh64.txt"))) {
		if strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		size, _ := strconv.Atoi(f[0])
		want, err := strconv.ParseUint(f[1], 16, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := sum64(buf[:size]); got != want {
			t.Errorf("%d bytes: %016x, want %016x", size, got, want)
		}
		for _, step := range []int{1, 3, 7, 31, 33} {
			var x xxh64
			x.reset()
			for b := buf[:size]; len(b) > 0; {
				k := min(step, len(b))
				x.write(b[:k])
				b = b[k:]
			}
			if got := x.sum(); got != want {
				t.Errorf("%d bytes in %d-byte writes: %016x, want %016x", size, step, got, want)
			}
		}
		n++
	}
	if n < 20 {
		t.Fatalf("read %d values", n)
	}
}
