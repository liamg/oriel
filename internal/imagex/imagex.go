// Package imagex decodes the pictures Oriel shows: PNG, JPEG, GIF, WebP, BMP and ICO, and a PNG's pixel density.
package imagex

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"path/filepath"
	"strings"

	"golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// imageExts are the files Decode decodes, by extension, and the format each is.
var imageExts = map[string]string{".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".gif": "gif", ".webp": "webp", ".bmp": "bmp", ".ico": "ico"}

// IsImage reports whether path is a picture by its extension: one Decode decodes, or an SVG.
func IsImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := imageExts[ext]
	return ok || ext == ".svg"
}

// Decode decodes a PNG, JPEG, GIF, WebP, BMP or ICO image.
func Decode(b []byte) (image.Image, string, error) {
	if len(b) >= 6 && binary.LittleEndian.Uint16(b) == 0 && binary.LittleEndian.Uint16(b[2:]) == 1 {
		img, err := decodeICO(b)
		return img, "ico", err
	}
	return image.Decode(bytes.NewReader(b))
}

// decodeICO decodes the largest image in an icon file: a PNG, or a bitmap without its file header.
func decodeICO(b []byte) (image.Image, error) {
	n := int(binary.LittleEndian.Uint16(b[4:]))
	best, area := -1, -1
	for i := range n {
		e := 6 + 16*i
		if e+16 > len(b) {
			break
		}
		w, h := int(b[e]), int(b[e+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		if w*h > area {
			best, area = i, w*h
		}
	}
	if best < 0 {
		return nil, errors.New("an icon with no images")
	}
	e := 6 + 16*best
	size, off := int(binary.LittleEndian.Uint32(b[e+8:])), int(binary.LittleEndian.Uint32(b[e+12:]))
	if off < 0 || size < 40 || off+size > len(b) {
		return nil, errors.New("a broken icon")
	}
	data := b[off : off+size]
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return png.Decode(bytes.NewReader(data))
	}
	// A bitmap's header says twice its height (the colours, then a mask); give it a file header and its own height.
	dib := bytes.Clone(data)
	h := int32(binary.LittleEndian.Uint32(dib[8:]))
	binary.LittleEndian.PutUint32(dib[8:], uint32(h/2))
	hsize := binary.LittleEndian.Uint32(dib)
	bpp := binary.LittleEndian.Uint16(dib[14:])
	colours := binary.LittleEndian.Uint32(dib[32:])
	if colours == 0 && bpp <= 8 {
		colours = 1 << bpp
	}
	file := make([]byte, 14, 14+len(dib))
	file[0], file[1] = 'B', 'M'
	binary.LittleEndian.PutUint32(file[2:], uint32(14+len(dib)))
	binary.LittleEndian.PutUint32(file[10:], 14+hsize+4*colours)
	img, err := bmp.Decode(bytes.NewReader(append(file, dib...)))
	if err != nil {
		return nil, fmt.Errorf("an icon's bitmap: %w", err)
	}
	return img, nil
}

// PNGDensity is a PNG's pixels per point from its pHYs chunk: a whole number for 144 DPI and up, else 0. 72 and 96
// DPI are both a screen's own pixels, so only a Retina-style density counts.
func PNGDensity(b []byte) float32 {
	const sig = 8
	for i := sig; i+8 <= len(b); {
		n, typ := int(binary.BigEndian.Uint32(b[i:])), string(b[i+4:i+8])
		data := b[i+8:]
		if n < 0 || n > len(data) || typ == "IDAT" {
			return 0
		}
		if typ == "pHYs" && n >= 9 && data[8] == 1 {
			dpi := float64(binary.BigEndian.Uint32(data)) * 0.0254
			if dpi < 140 {
				return 0
			}
			return float32(math.Round(dpi / 72))
		}
		i += 12 + n
	}
	return 0
}

// WithPNGDensity is the PNG b saying it has d pixels per point (2 for a Retina screenshot), in a pHYs chunk in place
// of any it had; b as it is when it isn't a PNG or d is under 1.
func WithPNGDensity(b []byte, d float32) []byte {
	const sig, ihdr = 8, 8 + 25 // the signature, then IHDR: its length, type, 13 bytes and CRC
	if d < 1 || len(b) < ihdr || !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) || string(b[sig+4:sig+8]) != "IHDR" {
		return b
	}
	out := append([]byte{}, b[:ihdr]...)
	ppm := uint32(math.Round(float64(d) * 72 / 0.0254))
	chunk := binary.BigEndian.AppendUint32(nil, 9)
	chunk = append(chunk, "pHYs"...)
	chunk = binary.BigEndian.AppendUint32(chunk, ppm)
	chunk = binary.BigEndian.AppendUint32(chunk, ppm)
	chunk = append(chunk, 1) // the unit: the metre
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	out = append(out, chunk...)
	for i := ihdr; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		if n < 0 || i+12+n > len(b) {
			return b
		}
		if string(b[i+4:i+8]) != "pHYs" {
			out = append(out, b[i:i+12+n]...)
		}
		i += 12 + n
	}
	return out
}
