package zstd

import (
	"encoding/binary"
	"math/bits"
)

// backward reads a bitstream from its end to its start (RFC 8878 4.1, 4.2.2). value holds the 8 bytes from off
// (or every byte, when there are fewer, with the missing high bytes counted as read); read counts the bits taken
// from its top, going over 64 once reads go past the start.
type backward struct {
	in    []byte
	off   int
	value uint64
	read  uint
}

func (b *backward) init(in []byte) error {
	if len(in) == 0 {
		return corrupt("empty bitstream")
	}
	last := in[len(in)-1]
	if last == 0 {
		return corrupt("bitstream without its end mark")
	}
	b.in = in
	if len(in) >= 8 {
		b.off = len(in) - 8
		b.value = binary.LittleEndian.Uint64(in[b.off:])
		b.read = uint(9 - bits.Len8(last))
		return nil
	}
	b.off = 0
	b.value = 0
	for i := len(in) - 1; i >= 0; i-- {
		b.value = b.value<<8 | uint64(in[i])
	}
	b.read = uint(64-8*len(in)) + uint(9-bits.Len8(last))
	return nil
}

// fill loads more bytes, leaving at most 7 bits read when at least 8 bytes are left before off.
func (b *backward) fill() {
	if b.off == 0 {
		return
	}
	n := int(b.read >> 3)
	if n > b.off {
		n = b.off
	}
	b.off -= n
	b.read -= uint(n) * 8
	b.value = binary.LittleEndian.Uint64(b.in[b.off:])
}

// get reads n bits, n < 64. Past the start it reads garbage, which overflow reports.
func (b *backward) get(n uint8) uint32 {
	v := b.value << (b.read & 63) >> 1 >> ((63 - uint(n)) & 63)
	b.read += uint(n)
	return uint32(v)
}

func (b *backward) overflow() bool { return b.off == 0 && b.read > 64 }

// done says every bit was read, and no more.
func (b *backward) done() bool { return b.off == 0 && b.read == 64 }

// forward reads a bitstream from its start, low bits first (RFC 8878 4.1.1). Bits past the end read as zero; past
// says when any were taken.
type forward struct {
	in  []byte
	pos int // in bits
}

func (f *forward) peek(n uint) uint32 {
	i := f.pos >> 3
	var v uint32
	for k := 0; k < 4 && i+k < len(f.in); k++ {
		v |= uint32(f.in[i+k]) << (8 * k)
	}
	return v >> (f.pos & 7) & (1<<n - 1)
}

func (f *forward) skip(n uint) { f.pos += int(n) }

func (f *forward) past() bool { return f.pos > len(f.in)*8 }
