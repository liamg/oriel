package kit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"path/filepath"
	"strings"

	"golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// imageExts are the files DecodeImage decodes, by extension, and the format each is.
var imageExts = map[string]string{".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".gif": "gif", ".webp": "webp", ".bmp": "bmp", ".ico": "ico"}

// IsImage reports whether path is a picture by its extension: one DecodeImage decodes, or an SVG.
func IsImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := imageExts[ext]
	return ok || ext == ".svg"
}

// DecodeImage decodes a PNG, JPEG, GIF, WebP, BMP or ICO image.
func DecodeImage(b []byte) (image.Image, string, error) {
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
