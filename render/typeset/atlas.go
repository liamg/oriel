package typeset

import "image"

// Entry is where an image sits in the atlas. Page -1 means it draws nothing.
type Entry struct {
	Page int
	Src  image.Rectangle
	Off  image.Point // from the pen (or the caller's origin) to Src.Min, in device pixels
}

type shelf struct{ y, h, x int }

// Page is one texture of the atlas. Version changes whenever something is added to it.
type Page struct {
	Img     *image.RGBA // premultiplied
	Version int
	shelves []shelf
	next    int // y of the next shelf
}

// Atlas packs small images (tinted glyphs, baked shapes) into fixed-size pages with a shelf packer.
// When every page is full it starts again with fresh pages and Gen increments, so pages already
// handed to the GPU are never overwritten.
type Atlas struct {
	PageSize int
	MaxPages int
	Pages    []*Page
	Gen      int
	entries  map[any]Entry
}

func NewAtlas(pageSize, maxPages int) *Atlas {
	return &Atlas{PageSize: pageSize, MaxPages: maxPages, entries: map[any]Entry{}}
}

func (a *Atlas) Len() int { return len(a.entries) }

func (a *Atlas) Lookup(k any) (Entry, bool) {
	e, ok := a.entries[k]
	return e, ok
}

// Add places a size-sized image that paint draws at at in dst, starting the atlas again if it's full.
// k must be comparable. An empty size, or one bigger than a page, is recorded as drawing nothing.
func (a *Atlas) Add(k any, size, off image.Point, paint func(dst *image.RGBA, at image.Point)) Entry {
	if size.X <= 0 || size.Y <= 0 {
		return a.none(k)
	}
	page, r, ok := a.place(size)
	if !ok {
		a.Reset()
		if page, r, ok = a.place(size); !ok {
			return a.none(k)
		}
	}
	p := a.Pages[page]
	paint(p.Img, r.Min)
	p.Version++
	e := Entry{Page: page, Src: r, Off: off}
	a.entries[k] = e
	return e
}

func (a *Atlas) none(k any) Entry {
	e := Entry{Page: -1}
	a.entries[k] = e
	return e
}

// Reset drops everything and starts new pages.
func (a *Atlas) Reset() {
	a.Pages = nil
	a.entries = map[any]Entry{}
	a.Gen++
}

const atlasPad = 1

func (a *Atlas) place(size image.Point) (int, image.Rectangle, bool) {
	w, h := size.X+atlasPad, size.Y+atlasPad
	if w > a.PageSize || h > a.PageSize {
		return 0, image.Rectangle{}, false
	}
	for pi, p := range a.Pages {
		for i := range p.shelves {
			s := &p.shelves[i]
			if h <= s.h && h*4 >= s.h*3 && s.x+w <= a.PageSize {
				r := image.Rect(s.x, s.y, s.x+size.X, s.y+size.Y)
				s.x += w
				return pi, r, true
			}
		}
		if p.next+h <= a.PageSize {
			p.shelves = append(p.shelves, shelf{y: p.next, h: h, x: w})
			r := image.Rect(0, p.next, size.X, p.next+size.Y)
			p.next += h
			return pi, r, true
		}
	}
	if len(a.Pages) >= a.MaxPages {
		return 0, image.Rectangle{}, false
	}
	a.Pages = append(a.Pages, &Page{Img: image.NewRGBA(image.Rect(0, 0, a.PageSize, a.PageSize))})
	return a.place(size)
}
