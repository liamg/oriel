package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// pictures are the images decoded from files and data: URIs, each kept while its file stays the same.
var pictures = &pictureCache{m: map[string]*picture{}}

type pictureCache struct {
	mu sync.Mutex
	m  map[string]*picture
}

// picture is a decoded image, its format ("png", "webp"…), and the file it came from as it was then.
type picture struct {
	img    image.Image
	format string
	err    error
	mod    time.Time
	size   int64
}

const maxPictures = 128

// imageExts are the files pictures decodes, by extension, and the format each is.
var imageExts = map[string]string{".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".gif": "gif", ".webp": "webp", ".bmp": "bmp", ".ico": "ico"}

// isImage reports whether path is a picture by its extension: one pictures decodes, or an SVG, which it can't.
func isImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := imageExts[ext]
	return ok || ext == ".svg"
}

var errSVG = errors.New("SVG isn't drawn: only raster images are")

// file is the image in the file at path, decoded once while the file's time and size stay the same.
func (p *pictureCache) file(path string) (image.Image, string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if strings.EqualFold(filepath.Ext(path), ".svg") {
		return nil, "svg", errSVG
	}
	p.mu.Lock()
	if pc, ok := p.m[path]; ok && pc.mod.Equal(fi.ModTime()) && pc.size == fi.Size() {
		p.mu.Unlock()
		return pc.img, pc.format, pc.err
	}
	p.mu.Unlock()
	pc := &picture{mod: fi.ModTime(), size: fi.Size()}
	if b, err := os.ReadFile(path); err != nil {
		pc.err = err
	} else {
		pc.img, pc.format, pc.err = decodeImage(b)
	}
	p.put(path, pc)
	return pc.img, pc.format, pc.err
}

// data is the image a data: URI holds, decoded once.
func (p *pictureCache) data(uri string) (image.Image, string, error) {
	sum := sha256.Sum256([]byte(uri))
	key := "data:" + string(sum[:])
	p.mu.Lock()
	if pc, ok := p.m[key]; ok {
		p.mu.Unlock()
		return pc.img, pc.format, pc.err
	}
	p.mu.Unlock()
	pc := &picture{}
	head, body, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	switch {
	case !ok:
		pc.err = errors.New("a data: URI with no data")
	case strings.HasPrefix(head, "image/svg"):
		pc.format, pc.err = "svg", errSVG
	default:
		var b []byte
		var err error
		if strings.HasSuffix(head, ";base64") {
			b, err = base64.StdEncoding.DecodeString(strings.TrimSpace(body))
		} else {
			var s string
			s, err = url.PathUnescape(body)
			b = []byte(s)
		}
		if err != nil {
			pc.err = err
		} else {
			pc.img, pc.format, pc.err = decodeImage(b)
		}
	}
	p.put(key, pc)
	return pc.img, pc.format, pc.err
}

func (p *pictureCache) put(key string, pc *picture) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.m) >= maxPictures {
		clear(p.m)
	}
	p.m[key] = pc
}

// decodeImage decodes a PNG, JPEG, GIF, WebP, BMP or ICO image.
func decodeImage(b []byte) (image.Image, string, error) {
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
