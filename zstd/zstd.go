// Package zstd decodes Zstandard (RFC 8878): frames of raw, RLE and compressed blocks, skippable frames, and any
// concatenation of them. A frame that needs a dictionary is refused.
package zstd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

const (
	frameMagic     = 0xFD2FB528
	skippableMagic = 0x184D2A50 // and the 15 values above it
	skippableMask  = 0xFFFFFFF0

	// MaxWindow is the largest window a frame may ask for: the history a reader keeps.
	MaxWindow = 1 << 27
)

var (
	// ErrCorrupt is wrapped by every error for input that isn't valid Zstandard.
	ErrCorrupt = errors.New("zstd: corrupt input")
	// ErrDictionary is a frame that names a dictionary.
	ErrDictionary = errors.New("zstd: frame needs a dictionary, which isn't supported")
	// ErrChecksum is a frame whose content doesn't match its checksum.
	ErrChecksum = errors.New("zstd: content checksum mismatch")
	// ErrWindow is a frame asking for a window larger than MaxWindow.
	ErrWindow = errors.New("zstd: window larger than MaxWindow")
	// ErrClosed is a read from a closed Reader.
	ErrClosed = errors.New("zstd: reader closed")
)

func corrupt(what string) error { return fmt.Errorf("%w: %s", ErrCorrupt, what) }

var errTruncated = fmt.Errorf("%w: truncated", ErrCorrupt)

// header is a frame header (RFC 8878 3.1.1.1).
type header struct {
	window   int
	size     uint64
	sized    bool
	checksum bool
}

// headerLen is the length of a frame header, from its first byte.
func headerLen(fhd byte) int {
	n := 1 + [4]int{0, 1, 2, 4}[fhd&3]
	single := fhd&0x20 != 0
	if !single {
		n++
	}
	switch fhd >> 6 {
	case 0:
		if single {
			n++
		}
	case 1:
		n += 2
	case 2:
		n += 4
	case 3:
		n += 8
	}
	return n
}

// parseHeader reads a frame header of headerLen(b[0]) bytes.
func parseHeader(b []byte) (header, error) {
	fhd := b[0]
	b = b[1:]
	var h header
	if fhd&0x08 != 0 {
		return h, corrupt("reserved frame header bit set")
	}
	h.checksum = fhd&0x04 != 0
	single := fhd&0x20 != 0
	var window uint64
	if !single {
		wd := b[0]
		b = b[1:]
		base := uint64(1) << (10 + wd>>3)
		window = base + base/8*uint64(wd&7)
	}
	var dict uint32
	switch fhd & 3 {
	case 1:
		dict, b = uint32(b[0]), b[1:]
	case 2:
		dict, b = uint32(binary.LittleEndian.Uint16(b)), b[2:]
	case 3:
		dict, b = binary.LittleEndian.Uint32(b), b[4:]
	}
	if dict != 0 {
		return h, fmt.Errorf("%w (dictionary %d)", ErrDictionary, dict)
	}
	switch fhd >> 6 {
	case 0:
		if single {
			h.size, h.sized = uint64(b[0]), true
		}
	case 1:
		h.size, h.sized = uint64(binary.LittleEndian.Uint16(b))+256, true
	case 2:
		h.size, h.sized = uint64(binary.LittleEndian.Uint32(b)), true
	case 3:
		h.size, h.sized = binary.LittleEndian.Uint64(b), true
	}
	if single {
		window = h.size
	}
	if window > MaxWindow {
		return h, fmt.Errorf("%w (%d bytes)", ErrWindow, window)
	}
	h.window = int(window)
	return h, nil
}

// Decode decodes every frame in src.
func Decode(src []byte) ([]byte, error) {
	var d decoder
	var out []byte
	for len(src) > 0 {
		if len(src) < 4 {
			return nil, errTruncated
		}
		magic := binary.LittleEndian.Uint32(src)
		src = src[4:]
		if magic&skippableMask == skippableMagic {
			if len(src) < 4 {
				return nil, errTruncated
			}
			n := uint64(binary.LittleEndian.Uint32(src))
			src = src[4:]
			if n > uint64(len(src)) {
				return nil, errTruncated
			}
			src = src[n:]
			continue
		}
		if magic != frameMagic {
			return nil, corrupt("not a zstd frame")
		}
		var err error
		if out, src, err = d.frame(out, src); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// frame decodes one frame after its magic number, appending it to out.
func (d *decoder) frame(out, src []byte) ([]byte, []byte, error) {
	if len(src) == 0 || len(src) < headerLen(src[0]) {
		return nil, nil, errTruncated
	}
	h, err := parseHeader(src)
	if err != nil {
		return nil, nil, err
	}
	src = src[headerLen(src[0]):]
	d.reset(h.window)
	start := len(out)
	if h.sized {
		out = growFor(out, h.size, len(src))
	}
	for {
		if len(src) < 3 {
			return nil, nil, errTruncated
		}
		bh := int(src[0]) | int(src[1])<<8 | int(src[2])<<16
		src = src[3:]
		typ, size := bh>>1&3, bh>>3
		n := size
		if typ == blockRLE {
			n = 1
		}
		if n > len(src) {
			return nil, nil, errTruncated
		}
		if out, err = d.block(out, start, typ, size, src[:n]); err != nil {
			return nil, nil, err
		}
		src = src[n:]
		if h.sized && uint64(len(out)-start) > h.size {
			return nil, nil, corrupt("frame larger than its content size")
		}
		if bh&1 != 0 {
			break
		}
	}
	if h.sized && uint64(len(out)-start) != h.size {
		return nil, nil, corrupt("frame smaller than its content size")
	}
	if h.checksum {
		if len(src) < 4 {
			return nil, nil, errTruncated
		}
		if uint32(sum64(out[start:])) != binary.LittleEndian.Uint32(src) {
			return nil, nil, ErrChecksum
		}
		src = src[4:]
	}
	return out, src, nil
}

// growFor makes room in out for a frame of size bytes, as far as in's length makes it plausible.
func growFor(out []byte, size uint64, in int) []byte {
	return slices.Grow(out, int(min(size, uint64(1<<20+64*in), MaxWindow)))
}
