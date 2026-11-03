package zstd

import (
	"encoding/binary"
	"slices"
)

const maxBlockSize = 128 << 10

// Block types (RFC 8878 3.1.1.2.2).
const (
	blockRaw = iota
	blockRLE
	blockCompressed
	blockReserved
)

// decoder is what a frame's blocks carry from one to the next (RFC 8878 3.1.1.3).
type decoder struct {
	window   int
	blockMax int
	rep      [3]int
	huf      huffTable
	hufOK    bool
	own      [3]seqTable  // the tables a block described
	cur      [3]*seqTable // the tables in use: own's, or predefined; nil before any
	lits     []byte
}

func (d *decoder) reset(window int) {
	d.window = window
	d.blockMax = min(window, maxBlockSize)
	d.rep = [3]int{1, 4, 8}
	d.hufOK = false
	d.cur = [3]*seqTable{}
	if d.lits == nil {
		d.lits = make([]byte, maxBlockSize)
	}
}

// block appends what a block decodes to out; out[start:] is the frame's history so far.
func (d *decoder) block(out []byte, start, typ, size int, in []byte) ([]byte, error) {
	if size > d.blockMax {
		return out, corrupt("block larger than the frame allows")
	}
	switch typ {
	case blockRaw:
		return append(out, in[:size]...), nil
	case blockRLE:
		out = slices.Grow(out, size)
		n := len(out)
		out = out[:n+size]
		fill(out[n:], in[0])
		return out, nil
	case blockCompressed:
		return d.compressed(out, start, in[:size])
	}
	return out, corrupt("reserved block type")
}

func fill(b []byte, c byte) {
	if len(b) == 0 {
		return
	}
	b[0] = c
	for n := 1; n < len(b); n *= 2 {
		copy(b[n:], b[:n])
	}
}

func (d *decoder) compressed(out []byte, start int, in []byte) ([]byte, error) {
	lits, n, err := d.literals(in)
	if err != nil {
		return out, err
	}
	in = in[n:]
	if len(in) == 0 {
		return out, corrupt("missing sequences section")
	}
	nseq := int(in[0])
	switch {
	case nseq == 0:
		if len(in) != 1 {
			return out, corrupt("data after an empty sequences section")
		}
		return append(out, lits...), nil
	case nseq < 128:
		in = in[1:]
	case nseq < 255:
		if len(in) < 2 {
			return out, corrupt("truncated sequences header")
		}
		nseq = (nseq-128)<<8 + int(in[1])
		in = in[2:]
	default:
		if len(in) < 3 {
			return out, corrupt("truncated sequences header")
		}
		nseq = int(in[1]) + int(in[2])<<8 + 0x7F00
		in = in[3:]
	}
	if nseq == 0 {
		if len(in) != 0 {
			return out, corrupt("data after an empty sequences section")
		}
		return append(out, lits...), nil
	}
	if len(in) == 0 {
		return out, corrupt("missing compression modes")
	}
	modes := in[0]
	in = in[1:]
	if modes&3 != 0 {
		return out, corrupt("reserved compression mode bits set")
	}
	for kind := range 3 {
		used, err := d.table(kind, modes>>(6-2*kind)&3, in)
		if err != nil {
			return out, err
		}
		in = in[used:]
	}
	return d.sequences(out, start, nseq, lits, in)
}

// literals decodes the literals section (RFC 8878 3.1.1.3.1), returning them and its size.
func (d *decoder) literals(in []byte) ([]byte, int, error) {
	if len(in) == 0 {
		return nil, 0, corrupt("missing literals section")
	}
	typ, format := in[0]&3, in[0]>>2&3
	if typ < 2 {
		var size, hl int
		switch format {
		case 0, 2:
			size, hl = int(in[0]>>3), 1
		case 1:
			if len(in) < 2 {
				return nil, 0, corrupt("truncated literals header")
			}
			size, hl = int(in[0]>>4)+int(in[1])<<4, 2
		case 3:
			if len(in) < 3 {
				return nil, 0, corrupt("truncated literals header")
			}
			size, hl = int(in[0]>>4)+int(in[1])<<4+int(in[2])<<12, 3
		}
		if size > d.blockMax {
			return nil, 0, corrupt("literals larger than a block")
		}
		if typ == 0 {
			if hl+size > len(in) {
				return nil, 0, corrupt("truncated literals")
			}
			return in[hl : hl+size], hl + size, nil
		}
		if hl+1 > len(in) {
			return nil, 0, corrupt("truncated literals")
		}
		lits := d.lits[:size]
		fill(lits, in[hl])
		return lits, hl + 1, nil
	}
	var regen, comp, hl int
	switch format {
	case 0, 1:
		if len(in) < 3 {
			return nil, 0, corrupt("truncated literals header")
		}
		v := int(in[0]) | int(in[1])<<8 | int(in[2])<<16
		regen, comp, hl = v>>4&0x3ff, v>>14&0x3ff, 3
	case 2:
		if len(in) < 4 {
			return nil, 0, corrupt("truncated literals header")
		}
		v := int(binary.LittleEndian.Uint32(in))
		regen, comp, hl = v>>4&0x3fff, v>>18&0x3fff, 4
	case 3:
		if len(in) < 5 {
			return nil, 0, corrupt("truncated literals header")
		}
		v := int(binary.LittleEndian.Uint32(in)) | int(in[4])<<32
		regen, comp, hl = v>>4&0x3ffff, v>>22&0x3ffff, 5
	}
	if regen > d.blockMax {
		return nil, 0, corrupt("literals larger than a block")
	}
	if hl+comp > len(in) {
		return nil, 0, corrupt("truncated literals")
	}
	src := in[hl : hl+comp]
	if typ == 2 {
		d.hufOK = false
		n, err := d.huf.read(src)
		if err != nil {
			return nil, 0, err
		}
		d.hufOK = true
		src = src[n:]
	} else if !d.hufOK {
		return nil, 0, corrupt("treeless literals with no earlier Huffman table")
	}
	lits := d.lits[:regen]
	var err error
	if format == 0 {
		err = d.huf.decode1(lits, src)
	} else {
		err = d.huf.decode4(lits, src)
	}
	return lits, hl + comp, err
}

// table sets up the decoding table of kind for mode (RFC 8878 3.1.1.3.2.1), returning the bytes its description took.
func (d *decoder) table(kind int, mode byte, in []byte) (int, error) {
	switch mode {
	case 0:
		d.cur[kind] = &predefined[kind]
		return 0, nil
	case 1:
		if len(in) == 0 {
			return 0, corrupt("missing RLE symbol")
		}
		if int(in[0]) > maxSymbol[kind] {
			return 0, corrupt("RLE symbol out of range")
		}
		d.own[kind].rle(kind, in[0])
		d.cur[kind] = &d.own[kind]
		return 1, nil
	case 2:
		var norm [256]int16
		nsym, log, used, err := readDist(in, maxSymbol[kind], maxLog[kind], &norm)
		if err != nil {
			return 0, err
		}
		d.cur[kind] = nil
		if err := d.own[kind].build(kind, norm[:nsym], log); err != nil {
			return 0, err
		}
		d.cur[kind] = &d.own[kind]
		return used, nil
	}
	if d.cur[kind] == nil {
		return 0, corrupt("repeat mode with no table to repeat")
	}
	return 0, nil
}

// sequences decodes and executes the block's sequences (RFC 8878 3.1.1.3.2.1.2, 3.1.1.4).
func (d *decoder) sequences(out []byte, start, nseq int, lits, in []byte) ([]byte, error) {
	var br backward
	if err := br.init(in); err != nil {
		return out, err
	}
	ll, of, ml := d.cur[litLen], d.cur[offCode], d.cur[matchLen]
	llMask, ofMask, mlMask := uint32(1)<<ll.log-1, uint32(1)<<of.log-1, uint32(1)<<ml.log-1
	llS := br.get(ll.log)
	ofS := br.get(of.log)
	mlS := br.get(ml.log)

	pos := len(out)
	limit := pos + d.blockMax
	out = slices.Grow(out, d.blockMax)[:limit]
	rep := d.rep
	window := d.window
	lp := 0
	for i := range nseq {
		le, oe, me := ll.e[llS&llMask], of.e[ofS&ofMask], ml.e[mlS&mlMask]
		br.fill()
		ov := int(oe.base) + int(br.get(oe.extra))
		br.fill()
		mlen := int(me.base) + int(br.get(me.extra))
		llen := int(le.base) + int(br.get(le.extra))
		if i < nseq-1 {
			br.fill()
			llS = uint32(le.next) + br.get(le.nb)
			mlS = uint32(me.next) + br.get(me.nb)
			ofS = uint32(oe.next) + br.get(oe.nb)
		}

		off := repeat(&rep, ov, llen)

		if llen > len(lits)-lp {
			return out[:pos], corrupt("sequence takes more literals than there are")
		}
		if llen+mlen > limit-pos {
			return out[:pos], corrupt("block decodes larger than the frame allows")
		}
		if lp+llen+16 <= len(lits) && pos+llen+16 <= limit {
			for k := 0; k < llen; k += 16 {
				*(*[16]byte)(out[pos+k:]) = *(*[16]byte)(lits[lp+k:])
			}
		} else {
			copy(out[pos:pos+llen], lits[lp:lp+llen])
		}
		pos += llen
		lp += llen
		if off <= 0 || off > pos-start || off > window {
			return out[:pos], corrupt("match offset out of range")
		}
		match(out, pos, off, mlen)
		pos += mlen
	}
	br.fill()
	if !br.done() {
		return out[:pos], corrupt("sequences bitstream not used exactly")
	}
	rest := lits[lp:]
	if len(rest) > limit-pos {
		return out[:pos], corrupt("block decodes larger than the frame allows")
	}
	pos += copy(out[pos:], rest)
	d.rep = rep
	return out[:pos], nil
}

// repeat resolves an offset value against the repeat offsets, and updates them (RFC 8878 3.1.1.5).
func repeat(rep *[3]int, ov, llen int) int {
	if ov > 3 {
		off := ov - 3
		rep[2], rep[1], rep[0] = rep[1], rep[0], off
		return off
	}
	if llen == 0 {
		ov++
	}
	switch ov {
	case 1:
		return rep[0]
	case 2:
		off := rep[1]
		rep[1], rep[0] = rep[0], off
		return off
	case 3:
		off := rep[2]
		rep[2], rep[1], rep[0] = rep[1], rep[0], off
		return off
	}
	off := rep[0] - 1
	rep[2], rep[1], rep[0] = rep[1], rep[0], off
	return off
}

// match copies n bytes from off back to out[pos:], the copy overlapping itself when off < n. With room past the
// end it copies in whole chunks, writing up to 15 bytes past n.
func match(out []byte, pos, off, n int) {
	src := pos - off
	if pos+n+16 <= len(out) {
		switch {
		case off >= 16:
			for k := 0; k < n; k += 16 {
				*(*[16]byte)(out[pos+k:]) = *(*[16]byte)(out[src+k:])
			}
			return
		case off >= 8:
			for k := 0; k < n; k += 8 {
				*(*[8]byte)(out[pos+k:]) = *(*[8]byte)(out[src+k:])
			}
			return
		}
	}
	if off >= n {
		copy(out[pos:pos+n], out[src:src+n])
		return
	}
	if off == 1 {
		fill(out[pos:pos+n], out[src])
		return
	}
	end := pos + n
	for pos < end {
		pos += copy(out[pos:end], out[src:pos])
	}
}
