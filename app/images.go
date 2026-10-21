package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"

	"github.com/liamg/oriel/render/svg"
)

// pictures are the images decoded from files and data: URIs, each kept while its file stays the same.
var pictures = &pictureCache{m: map[string]*picture{}}

type pictureCache struct {
	mu sync.Mutex
	m  map[string]*picture
}

// picture is a decoded image or an SVG's drawing, its format ("png", "svg"…), and the file it came from as it was
// then. A remote one is loading until it's fetched.
type picture struct {
	img     image.Image
	doc     *svg.Doc
	format  string
	err     error
	mod     time.Time
	size    int64
	loading bool
}

const maxPictures = 128

// imageExts are the files pictures decodes, by extension, and the format each is.
var imageExts = map[string]string{".png": "png", ".jpg": "jpeg", ".jpeg": "jpeg", ".gif": "gif", ".webp": "webp", ".bmp": "bmp", ".ico": "ico"}

// isImage reports whether path is a picture by its extension: one pictures decodes, or an SVG.
func isImage(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	_, ok := imageExts[ext]
	return ok || ext == ".svg"
}

// fileEntry is the picture in the file at path: an image, or an SVG's drawing.
func (p *pictureCache) fileEntry(path string) *picture {
	fi, err := os.Stat(path)
	if err != nil {
		return &picture{err: err}
	}
	p.mu.Lock()
	if pc, ok := p.m[path]; ok && pc.mod.Equal(fi.ModTime()) && pc.size == fi.Size() {
		p.mu.Unlock()
		return pc
	}
	p.mu.Unlock()
	pc := &picture{mod: fi.ModTime(), size: fi.Size()}
	if b, err := os.ReadFile(path); err != nil {
		pc.err = err
	} else {
		pc.decode(b)
	}
	p.put(path, pc)
	return pc
}

// dataEntry is the picture a data: URI holds, decoded once.
func (p *pictureCache) dataEntry(uri string) *picture {
	sum := sha256.Sum256([]byte(uri))
	key := "data:" + string(sum[:])
	p.mu.Lock()
	if pc, ok := p.m[key]; ok {
		p.mu.Unlock()
		return pc
	}
	p.mu.Unlock()
	pc := &picture{}
	head, body, ok := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !ok {
		pc.err = errors.New("a data: URI with no data")
	} else {
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
			pc.decode(b)
		}
	}
	p.put(key, pc)
	return pc
}

// fetchPicture gets a remote image's bytes.
var fetchPicture = func(ctx context.Context, src string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/*")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the server answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxRemote+1))
	if err == nil && len(b) > maxRemote {
		err = errors.New("the image is over 20 MB")
	}
	return b, err
}

const (
	maxRemote     = 20 << 20
	remoteTimeout = 30 * time.Second
)

// pictureWake asks for a frame when a remote image has loaded: the window's.
var pictureWake atomic.Pointer[func()]

// remote is the image at an http(s) URL. The first ask starts fetching it in the background and answers a picture
// still loading; the window is woken when it's in.
func (p *pictureCache) remote(src string) *picture {
	key := "remote:" + src
	p.mu.Lock()
	pc, ok := p.m[key]
	if !ok {
		pc = &picture{loading: true}
		p.putLocked(key, pc)
	}
	p.mu.Unlock()
	if ok {
		return pc
	}
	fetch := fetchPicture
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		got := &picture{}
		if b, err := fetch(ctx, src); err != nil {
			got.err = err
		} else {
			got.decode(b)
		}
		p.put(key, got)
		if wake := pictureWake.Load(); wake != nil {
			(*wake)()
		}
	}()
	return pc
}

// decode reads b as an SVG when it's markup, else as a raster image.
func (pc *picture) decode(b []byte) {
	if looksSVG(b) {
		pc.format = "svg"
		pc.doc, pc.err = svg.Parse(b)
		return
	}
	pc.img, pc.format, pc.err = decodeImage(b)
}

// looksSVG reports whether b is markup with an <svg> element near its start, past any XML declaration or doctype.
func looksSVG(b []byte) bool {
	b = bytes.TrimLeft(b, "\ufeff \t\r\n")
	return bytes.HasPrefix(b, []byte("<")) && bytes.Contains(bytes.ToLower(b[:min(len(b), 1024)]), []byte("<svg"))
}

func (p *pictureCache) put(key string, pc *picture) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.putLocked(key, pc)
}

func (p *pictureCache) putLocked(key string, pc *picture) {
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
