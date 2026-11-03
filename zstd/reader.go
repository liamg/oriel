package zstd

import (
	"bufio"
	"encoding/binary"
	"io"
)

// Reader decodes a stream of frames as it's read, keeping only a frame's window of history.
type Reader struct {
	src   *bufio.Reader
	d     decoder
	hist  []byte // the window's history, then what Read has yet to return
	pos   int    // hist[pos:] is yet to be returned
	in    bool   // in a frame, with blocks to come
	h     header
	n     uint64 // the frame's bytes so far
	sum   xxh64
	block []byte
	err   error
}

// NewReader reads frames from r, reading the first frame's header at once.
func NewReader(r io.Reader) (*Reader, error) {
	z := &Reader{src: bufio.NewReaderSize(r, 64<<10)}
	if err := z.next(); err != nil {
		if err != io.EOF {
			return nil, err
		}
		z.err = err
	}
	return z, nil
}

// Read implements io.Reader.
func (z *Reader) Read(p []byte) (int, error) {
	for {
		if z.pos < len(z.hist) {
			n := copy(p, z.hist[z.pos:])
			z.pos += n
			return n, nil
		}
		if z.err != nil {
			return 0, z.err
		}
		if len(p) == 0 {
			return 0, nil
		}
		if z.in {
			z.err = z.step()
		} else {
			z.err = z.next()
		}
	}
}

// Close drops the reader's buffers; it doesn't close what it reads.
func (z *Reader) Close() error {
	z.hist, z.block, z.pos = nil, nil, 0
	z.err = ErrClosed
	return nil
}

// full reads len(b) bytes, the input ending in their midst being a truncated frame.
func (z *Reader) full(b []byte) error {
	if _, err := io.ReadFull(z.src, b); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return errTruncated
		}
		return err
	}
	return nil
}

// next starts the next frame, skipping skippable ones; io.EOF when the input ends between frames.
func (z *Reader) next() error {
	var b [18]byte
	for {
		if _, err := io.ReadFull(z.src, b[:4]); err != nil {
			if err == io.ErrUnexpectedEOF {
				return errTruncated
			}
			return err
		}
		magic := binary.LittleEndian.Uint32(b[:])
		if magic&skippableMask == skippableMagic {
			if err := z.full(b[:4]); err != nil {
				return err
			}
			n := int64(binary.LittleEndian.Uint32(b[:]))
			if m, err := io.CopyN(io.Discard, z.src, n); m < n {
				if err == io.EOF || err == nil {
					return errTruncated
				}
				return err
			}
			continue
		}
		if magic != frameMagic {
			return corrupt("not a zstd frame")
		}
		if err := z.full(b[:1]); err != nil {
			return err
		}
		n := headerLen(b[0])
		if err := z.full(b[1:n]); err != nil {
			return err
		}
		h, err := parseHeader(b[:n])
		if err != nil {
			return err
		}
		z.h, z.n, z.in = h, 0, true
		z.d.reset(h.window)
		z.hist, z.pos = z.hist[:0], 0
		z.sum.reset()
		return nil
	}
}

// step decodes the frame's next block, and checks the frame once it's done.
func (z *Reader) step() error {
	z.slide()
	var bh [3]byte
	if err := z.full(bh[:]); err != nil {
		return err
	}
	v := int(bh[0]) | int(bh[1])<<8 | int(bh[2])<<16
	typ, size := v>>1&3, v>>3
	n := size
	if typ == blockRLE {
		n = 1
	}
	if n > z.d.blockMax {
		return corrupt("block larger than the frame allows")
	}
	if cap(z.block) < n {
		z.block = make([]byte, max(n, min(maxBlockSize, z.d.blockMax)))
	}
	blk := z.block[:n]
	if err := z.full(blk); err != nil {
		return err
	}
	before := len(z.hist)
	var err error
	if z.hist, err = z.d.block(z.hist, 0, typ, size, blk); err != nil {
		z.hist = z.hist[:before]
		return err
	}
	if z.h.checksum {
		z.sum.write(z.hist[before:])
	}
	z.n += uint64(len(z.hist) - before)
	if z.h.sized && z.n > z.h.size {
		return corrupt("frame larger than its content size")
	}
	if v&1 == 0 {
		return nil
	}
	z.in = false
	if z.h.sized && z.n != z.h.size {
		return corrupt("frame smaller than its content size")
	}
	if z.h.checksum {
		var c [4]byte
		if err := z.full(c[:]); err != nil {
			return err
		}
		if uint32(z.sum.sum()) != binary.LittleEndian.Uint32(c[:]) {
			return ErrChecksum
		}
	}
	return nil
}

// slide drops history older than the window once enough has built up, all of it having been read.
func (z *Reader) slide() {
	keep := z.d.window
	over := len(z.hist) - keep
	if over < max(keep, 256<<10) {
		return
	}
	copy(z.hist, z.hist[over:])
	z.hist = z.hist[:keep]
	z.pos -= over
}
