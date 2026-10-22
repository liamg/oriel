package zstd

import (
	"encoding/binary"
	"math/bits"
)

const maxHuffBits = 11

// huffTable decodes literals (RFC 8878 4.2): indexed by the next bits of the stream, each entry is a symbol<<8 and
// the length of its code.
type huffTable struct {
	bits uint8
	dt   [1 << maxHuffBits]uint16
}

// read reads a Huffman tree description (RFC 8878 4.2.1) and returns how many bytes it took.
func (h *huffTable) read(in []byte) (int, error) {
	if len(in) == 0 {
		return 0, corrupt("missing Huffman tree description")
	}
	var w [256]byte
	var n, used int
	if hb := int(in[0]); hb >= 128 {
		n = hb - 127
		used = 1 + (n+1)/2
		if used > len(in) {
			return 0, corrupt("truncated Huffman weights")
		}
		for i := range n {
			b := in[1+i/2]
			if i%2 == 0 {
				w[i] = b >> 4
			} else {
				w[i] = b & 15
			}
		}
	} else {
		used = 1 + hb
		if used > len(in) {
			return 0, corrupt("truncated Huffman weights")
		}
		var err error
		if n, err = fseWeights(in[1:used], &w); err != nil {
			return 0, err
		}
	}
	return used, h.build(w[:n:256])
}

// build makes the table from the weights of every symbol but the last, whose weight completes theirs to a power of 2.
func (h *huffTable) build(w []byte) error {
	n := len(w)
	if n == 0 || n > 255 {
		return corrupt("bad number of Huffman weights")
	}
	sum := 0
	var rank [maxHuffBits + 2]int
	for _, x := range w {
		if x > maxHuffBits {
			return corrupt("Huffman weight too large")
		}
		if x > 0 {
			sum += 1 << (x - 1)
		}
		rank[x]++
	}
	if sum == 0 {
		return corrupt("Huffman weights all zero")
	}
	maxBits := bits.Len(uint(sum))
	if maxBits > maxHuffBits {
		return corrupt("Huffman code too long")
	}
	rest := 1<<maxBits - sum
	if rest&(rest-1) != 0 {
		return corrupt("Huffman weights don't complete to a power of 2")
	}
	last := byte(bits.Len(uint(rest)))
	rank[last]++
	var start [maxHuffBits + 2]int
	pos := 0
	for x := 1; x <= maxBits; x++ {
		start[x] = pos
		pos += rank[x] << (x - 1)
	}
	put := func(s int, x byte) {
		e := uint16(s)<<8 | uint16(maxBits+1-int(x))
		p := start[x]
		for i := range 1 << (x - 1) {
			h.dt[p+i] = e
		}
		start[x] = p + 1<<(x-1)
	}
	for s, x := range w {
		if x > 0 {
			put(s, x)
		}
	}
	put(n, last)
	h.bits = uint8(maxBits)
	return nil
}

// fseWeights decodes FSE-compressed Huffman weights (RFC 8878 4.2.1.2): two states take turns over one bitstream
// until it runs out.
func fseWeights(in []byte, w *[256]byte) (int, error) {
	var norm [256]int16
	nsym, log, used, err := readDist(in, maxHuffBits+1, 6, &norm)
	if err != nil {
		return 0, err
	}
	var dt [64]fseEntry
	if err := buildFSE(norm[:nsym], log, dt[:1<<log]); err != nil {
		return 0, err
	}
	var br backward
	if err := br.init(in[used:]); err != nil {
		return 0, err
	}
	mask := uint32(1<<log - 1)
	s1 := br.get(uint8(log))
	s2 := br.get(uint8(log))
	n := 0
	for {
		if n > 253 {
			return 0, corrupt("too many Huffman weights")
		}
		e := dt[s1&mask]
		w[n] = e.sym
		n++
		s1 = uint32(e.next) + br.get(e.nb)
		br.fill()
		if br.overflow() {
			w[n] = dt[s2&mask].sym
			return n + 1, nil
		}
		e = dt[s2&mask]
		w[n] = e.sym
		n++
		s2 = uint32(e.next) + br.get(e.nb)
		br.fill()
		if br.overflow() {
			w[n] = dt[s1&mask].sym
			return n + 1, nil
		}
	}
}

// decode1 decodes one Huffman-coded stream (RFC 8878 4.2.2) filling dst.
func (h *huffTable) decode1(dst, in []byte) error {
	var br backward
	if err := br.init(in); err != nil {
		return err
	}
	return h.stream(dst, &br)
}

// decode4 decodes four streams after their jump table (RFC 8878 3.1.1.3.1.6).
func (h *huffTable) decode4(dst, in []byte) error {
	if len(in) < 6 {
		return corrupt("truncated jump table")
	}
	s1 := int(binary.LittleEndian.Uint16(in))
	s2 := int(binary.LittleEndian.Uint16(in[2:]))
	s3 := int(binary.LittleEndian.Uint16(in[4:]))
	in = in[6:]
	if s1+s2+s3 > len(in) {
		return corrupt("jump table past the literals")
	}
	seg := (len(dst) + 3) / 4
	if 3*seg > len(dst) {
		return corrupt("too few literals for four streams")
	}
	streams := [4][]byte{in[:s1], in[s1 : s1+s2], in[s1+s2 : s1+s2+s3], in[s1+s2+s3:]}
	for i, s := range streams {
		end := (i + 1) * seg
		if i == 3 {
			end = len(dst)
		}
		if err := h.decode1(dst[i*seg:end], s); err != nil {
			return err
		}
	}
	return nil
}

// stream decodes len(dst) symbols, which must use the whole stream.
func (h *huffTable) stream(dst []byte, br *backward) error {
	shift := 64 - uint(h.bits)
	dt := &h.dt
	i := 0
	// While 8 bytes are left to load, a fill leaves at least 57 bits: five codes' worth.
	for br.off >= 8 && i+4 <= len(dst) {
		br.fill()
		v, r := br.value, br.read
		e := dt[(v<<r>>shift)&(1<<maxHuffBits-1)]
		r += uint(e & 0xff)
		e1 := dt[(v<<r>>shift)&(1<<maxHuffBits-1)]
		r += uint(e1 & 0xff)
		e2 := dt[(v<<r>>shift)&(1<<maxHuffBits-1)]
		r += uint(e2 & 0xff)
		e3 := dt[(v<<r>>shift)&(1<<maxHuffBits-1)]
		r += uint(e3 & 0xff)
		br.read = r
		d := dst[i : i+4 : i+4]
		d[0], d[1], d[2], d[3] = byte(e>>8), byte(e1>>8), byte(e2>>8), byte(e3>>8)
		i += 4
	}
	for ; i < len(dst); i++ {
		br.fill()
		e := dt[(br.value<<br.read>>shift)&(1<<maxHuffBits-1)]
		br.read += uint(e & 0xff)
		dst[i] = byte(e >> 8)
	}
	br.fill()
	if !br.done() {
		return corrupt("Huffman stream not used exactly")
	}
	return nil
}
