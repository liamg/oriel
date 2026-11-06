package editor

import (
	"image"
	"image/color"
	"net/http"
	"testing"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/vim"
)

// What the package's outside tests read and set of its plugins.

func (x *Editor) Engine() *vim.Engine { return x.e }

// Wait waits for the parsers being made and the files being read off the loop.
func (x *Editor) Wait() { x.busy.Wait() }

// WaitDocs waits for every buffer's parse to catch up.
func (x *Editor) WaitDocs() {
	for _, h := range x.docs {
		if h.doc != nil {
			h.doc.Wait()
		}
	}
}

func (x *Editor) Current() vim.Pane        { return x.current() }
func (x *Editor) Metrics() kit.PaneMetrics { return x.pane }

// MdPicture is the picture a markdown image's src names, found from dir, or why there's none.
func MdPicture(pics *kit.Pictures, src, dir string) (*kit.Picture, string) {
	return mdPicture(pics, src, dir)
}

func PNGBytes(t *testing.T, img image.Image) []byte          { return pngBytes(t, img) }
func SolidImage(w, h int, c color.NRGBA) *image.NRGBA        { return solidImage(w, h, c) }
func PicturesFor(t *testing.T, c *http.Client) *kit.Pictures { return testPictures(t, c) }

// Badge is a 90×20 badge in shields.io's flat style.
const Badge = testBadge

// Decorations are the decorations of the lines [top, bottom) of a pane's buffer, by line.
func (x *Editor) Decorations(p vim.Pane, top, bottom int) map[int][]uireg.Decoration {
	return x.decorations(p, top, bottom, nil)
}

// Rows and BlockPx are how many rows window id has for text, and how tall the blocks between its lines were, last
// frame.
func (x *Editor) Rows(id vim.WindowID) int        { return x.rows[id] }
func (x *Editor) BlockPx(id vim.WindowID) float32 { return x.blockPx[id] }

// RefreshConventions reads the conventions again for the open buffers.
func (x *Editor) RefreshConventions() { x.conv.refresh() }

// Searchable are names less those the conventions keep out of search.
func (x *Editor) Searchable(names []string) []string { return x.conv.searchable(names) }

// BaseReads counts the bases the gutter has read, each file's index or commit copy.
func (g *GitSigns) BaseReads() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.gens
}
