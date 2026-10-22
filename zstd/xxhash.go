package zstd

import (
	"encoding/binary"
	"math/bits"
)

// XXH64 with a seed of zero: the content checksum's hash (RFC 8878 3.1.1).

const (
	prime1 uint64 = 11400714785074694791
	prime2 uint64 = 14029467366897019727
	prime3 uint64 = 1609587929392839161
	prime4 uint64 = 9650029242287828579
	prime5 uint64 = 2870177450012600261
)

func round(acc, lane uint64) uint64 {
	return bits.RotateLeft64(acc+lane*prime2, 31) * prime1
}

func merge(h, v uint64) uint64 {
	return (h^round(0, v))*prime1 + prime4
}

type xxh64 struct {
	v     [4]uint64
	mem   [32]byte
	n     int // bytes in mem
	total uint64
}

func (x *xxh64) reset() {
	p1 := prime1
	*x = xxh64{v: [4]uint64{p1 + prime2, prime2, 0, -p1}}
}

// stripes takes in b's whole 32-byte stripes and returns what's left.
func (x *xxh64) stripes(b []byte) []byte {
	v0, v1, v2, v3 := x.v[0], x.v[1], x.v[2], x.v[3]
	for ; len(b) >= 32; b = b[32:] {
		v0 = round(v0, binary.LittleEndian.Uint64(b[0:]))
		v1 = round(v1, binary.LittleEndian.Uint64(b[8:]))
		v2 = round(v2, binary.LittleEndian.Uint64(b[16:]))
		v3 = round(v3, binary.LittleEndian.Uint64(b[24:]))
	}
	x.v = [4]uint64{v0, v1, v2, v3}
	return b
}

func (x *xxh64) write(b []byte) {
	x.total += uint64(len(b))
	if x.n > 0 {
		k := copy(x.mem[x.n:], b)
		x.n += k
		b = b[k:]
		if x.n < 32 {
			return
		}
		x.stripes(x.mem[:])
		x.n = 0
	}
	b = x.stripes(b)
	x.n = copy(x.mem[:], b)
}

func (x *xxh64) sum() uint64 {
	var h uint64
	if x.total >= 32 {
		v := x.v
		h = bits.RotateLeft64(v[0], 1) + bits.RotateLeft64(v[1], 7) + bits.RotateLeft64(v[2], 12) + bits.RotateLeft64(v[3], 18)
		for _, l := range v {
			h = merge(h, l)
		}
	} else {
		h = prime5
	}
	h += x.total
	b := x.mem[:x.n]
	for ; len(b) >= 8; b = b[8:] {
		h ^= round(0, binary.LittleEndian.Uint64(b))
		h = bits.RotateLeft64(h, 27)*prime1 + prime4
	}
	if len(b) >= 4 {
		h ^= uint64(binary.LittleEndian.Uint32(b)) * prime1
		h = bits.RotateLeft64(h, 23)*prime2 + prime3
		b = b[4:]
	}
	for _, c := range b {
		h ^= uint64(c) * prime5
		h = bits.RotateLeft64(h, 11) * prime1
	}
	h ^= h >> 33
	h *= prime2
	h ^= h >> 29
	h *= prime3
	h ^= h >> 32
	return h
}

func sum64(b []byte) uint64 {
	var x xxh64
	x.reset()
	x.write(b)
	return x.sum()
}
