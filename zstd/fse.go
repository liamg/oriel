package zstd

import "math/bits"

// fseEntry is one state of an FSE decoding table (RFC 8878 4.1): the symbol it decodes, and how to reach the next
// state: read nb bits and add them to next.
type fseEntry struct {
	sym  uint8
	nb   uint8
	next uint16
}

// readDist reads an FSE table description (RFC 8878 4.1.1) of symbols up to maxSym into norm, returning how many
// symbols it gives, its accuracy log and how many bytes it took.
func readDist(in []byte, maxSym, maxLog int, norm *[256]int16) (nsym, log, used int, err error) {
	if len(in) == 0 {
		return 0, 0, 0, corrupt("missing FSE table description")
	}
	f := forward{in: in}
	log = int(in[0]&15) + 5
	if log > maxLog {
		return 0, 0, 0, corrupt("FSE accuracy log too large")
	}
	f.skip(4)
	remaining := 1<<log + 1
	threshold := 1 << log
	nb := uint(log + 1)
	sym := 0
	for remaining > 1 && sym <= maxSym {
		limit := 2*threshold - 1 - remaining
		v := int(f.peek(nb))
		count := v & (threshold - 1)
		if count < limit {
			f.skip(nb - 1)
		} else {
			count = v & (2*threshold - 1)
			if count >= threshold {
				count -= limit
			}
			f.skip(nb)
		}
		count--
		if count < 0 {
			remaining--
		} else {
			remaining -= count
		}
		norm[sym] = int16(count)
		sym++
		if count == 0 {
			for {
				rep := int(f.peek(2))
				f.skip(2)
				if sym+rep > maxSym+1 {
					return 0, 0, 0, corrupt("FSE table description has too many symbols")
				}
				for range rep {
					norm[sym] = 0
					sym++
				}
				if rep != 3 || f.past() {
					break
				}
			}
		}
		for remaining < threshold {
			nb--
			threshold >>= 1
		}
		if f.past() {
			break
		}
	}
	if remaining != 1 || f.past() {
		return 0, 0, 0, corrupt("bad FSE table description")
	}
	return sym, log, (f.pos + 7) / 8, nil
}

// buildFSE makes the decoding table of norm's distribution at accuracy log, into dt (1<<log entries).
func buildFSE(norm []int16, log int, dt []fseEntry) error {
	size := 1 << log
	high := size - 1
	var next [256]uint16
	for s, c := range norm {
		if c == -1 {
			if high < 0 {
				return corrupt("FSE distribution overfull")
			}
			dt[high].sym = uint8(s)
			high--
			next[s] = 1
		} else {
			next[s] = uint16(c)
		}
	}
	step := size>>1 + size>>3 + 3
	mask := size - 1
	pos, filled := 0, 0
	for s, c := range norm {
		for range int(c) {
			if filled > high {
				return corrupt("FSE distribution overfull")
			}
			dt[pos].sym = uint8(s)
			filled++
			pos = (pos + step) & mask
			for pos > high {
				pos = (pos + step) & mask
			}
		}
	}
	if pos != 0 || filled != high+1 {
		return corrupt("FSE distribution doesn't fill its table")
	}
	for u := range dt[:size] {
		s := dt[u].sym
		ns := next[s]
		next[s]++
		nb := log - (bits.Len16(ns) - 1)
		dt[u].nb = uint8(nb)
		dt[u].next = uint16(int(ns)<<nb - size)
	}
	return nil
}

// seqEntry is a state of a sequences table, with its code's baseline and extra bits.
type seqEntry struct {
	base  uint32
	extra uint8
	nb    uint8
	next  uint16
}

type seqTable struct {
	log uint8
	e   [512]seqEntry
}

// The three symbol types of a sequence, in the order their tables are described.
const (
	litLen = iota
	offCode
	matchLen
)

var (
	maxSymbol = [3]int{35, 31, 52}
	maxLog    = [3]int{9, 8, 9}

	litLenBase = [36]uint32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
		16, 18, 20, 22, 24, 28, 32, 40, 48, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536}
	litLenBits = [36]uint8{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		1, 1, 1, 1, 2, 2, 3, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	matchLenBase = [53]uint32{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
		27, 28, 29, 30, 31, 32, 33, 34, 35, 37, 39, 41, 43, 47, 51, 59, 67, 83, 99, 131, 259, 515, 1027, 2051, 4099,
		8195, 16387, 32771, 65539}
	matchLenBits = [53]uint8{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 1, 1, 1, 1, 2, 2, 3, 3, 4, 4, 5, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}

	// The default distributions (RFC 8878 3.1.1.3.2.2) and their accuracy logs.
	defaultDist = [3][]int16{
		{4, 3, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 3, 2, 1, 1, 1, 1, 1, -1, -1, -1, -1},
		{1, 1, 1, 1, 1, 1, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1},
		{1, 4, 3, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
			1, 1, 1, 1, 1, 1, 1, 1, 1, -1, -1, -1, -1, -1, -1, -1},
	}
	defaultLog = [3]int{6, 5, 6}

	// predefined are the tables of the default distributions, built once.
	predefined = func() [3]seqTable {
		var t [3]seqTable
		for kind := range t {
			if err := t[kind].build(kind, defaultDist[kind], defaultLog[kind]); err != nil {
				panic(err)
			}
		}
		return t
	}()
)

// build makes t the table of a distribution of kind's codes.
func (t *seqTable) build(kind int, norm []int16, log int) error {
	var dt [512]fseEntry
	if err := buildFSE(norm, log, dt[:1<<log]); err != nil {
		return err
	}
	t.log = uint8(log)
	for u, e := range dt[:1<<log] {
		t.e[u] = code(kind, e.sym)
		t.e[u].nb, t.e[u].next = e.nb, e.next
	}
	return nil
}

// rle makes t a table of one state, sym.
func (t *seqTable) rle(kind int, sym uint8) {
	t.log = 0
	t.e[0] = code(kind, sym)
}

func code(kind int, sym uint8) seqEntry {
	switch kind {
	case litLen:
		return seqEntry{base: litLenBase[sym], extra: litLenBits[sym]}
	case matchLen:
		return seqEntry{base: matchLenBase[sym], extra: matchLenBits[sym]}
	}
	return seqEntry{base: 1 << sym, extra: sym}
}
