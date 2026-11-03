package editor

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/svg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ImageView is the view a file the image plugin claims opens in: its instance is the file's absolute path.
const ImageView = "image"

const imageHelp = "files/image"

var tImageViewBg = theme.Declare("image.view.bg", "bg.base")

// Images is the built-in file viewer for what isn't text: images drawn fit to their tab, and other binary files
// (video, audio, PDFs) as a card that opens them with the OS. It's written as a plugin would be.
type Images struct {
	kit.Disposer
	h plugin.Host
}

// viewedFile is a file as its tab reads it, off the loop.
type viewedFile struct {
	img     image.Image
	doc     *svg.Doc
	format  string
	err     error
	size    int64
	loading bool
}

func (x *Images) Manifest() plugin.Manifest { return plugin.Manifest{ID: "image"} }

func (x *Images) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.Add(h.View(ImageView, imageView{x}))
	x.Add(h.Contribute(registry.FileViewer{Name: "image", When: isMedia, View: ImageView, Order: 50, Help: imageHelp, Source: "builtin:image"}))
	return nil
}

// mediaKinds are files that aren't text by their extension, and what each is.
var mediaKinds = map[string]string{".mp4": "video", ".mov": "video", ".webm": "video", ".mkv": "video", ".avi": "video", ".m4v": "video",
	".mp3": "audio", ".wav": "audio", ".flac": "audio", ".ogg": "audio", ".m4a": "audio", ".aac": "audio", ".opus": "audio",
	".pdf": "PDF document", ".zip": "archive", ".gz": "archive", ".tgz": "archive", ".tar": "archive", ".7z": "archive",
	".dmg": "disk image", ".iso": "disk image", ".woff": "font", ".woff2": "font", ".ttf": "font", ".otf": "font", ".psd": "image", ".tiff": "image",
	".heic": "image", ".avif": "image"}

// isMedia reports whether the image plugin shows path: an image, a file of a kind that isn't text, or one whose start
// holds a NUL byte.
func isMedia(path string) bool {
	if kit.IsImage(path) || mediaKinds[strings.ToLower(filepath.Ext(path))] != "" {
		return true
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	b := make([]byte, 8000)
	n, _ := f.Read(b)
	return kit.IsBinary(b[:n])
}

// fileKind is what a file is, for its card: "PNG image", "video", "binary file".
func fileKind(path, format string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case format != "":
		return strings.ToUpper(format) + " image"
	case ext == ".svg":
		return "SVG image"
	case mediaKinds[ext] != "":
		return strings.ToUpper(strings.TrimPrefix(ext, ".")) + " " + mediaKinds[ext]
	}
	return "binary file"
}

// file is path as it is on disk, read through the window's reads: its tab follows the file.
func (x *Images) file(c *ui.Ctx, path string) *viewedFile {
	fi, st := kit.ReadsIn(c).Stat(path)
	f := &viewedFile{loading: st.Loading()}
	if fi == nil {
		f.err = st.Err
		return f
	}
	f.size = fi.Size()
	if kit.IsImage(path) {
		pc := kit.PicturesOf(c).File(path)
		f.img, f.doc, f.format, f.err, f.loading = pc.Img, pc.Doc, pc.Format, pc.Err, pc.Loading
	}
	return f
}

func (x *Images) view(c *ui.Ctx, vc uireg.ViewCtx) {
	th := c.Theme()
	path := vc.Instance
	f := x.file(c, path)
	name := filepath.Base(path)
	c.Column("image", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(24), Gap: 12, Main: ui.Center, Cross: ui.Center, Fill: th.C(tImageViewBg),
		Clip: true, Role: ui.RoleRegion, Label: name, Help: imageHelp}, func(c *ui.Ctx) {
		var size render.Point
		switch {
		case f.img != nil:
			b := f.img.Bounds()
			size = render.Point{X: float32(b.Dx()), Y: float32(b.Dy())}
		case f.doc != nil:
			size = f.doc.Size
		case f.loading:
			return
		default:
			x.card(c, path, f)
			return
		}
		availW, availH := max(vc.Width-48, 16), max(vc.Height-48-12-16, 16)
		w := min(size.X, availW, availH*size.X/max(size.Y, 1))
		o := widget.ImageOpts{Label: name, Help: imageHelp, Zoom: true, Group: "file:" + path}
		if f.img != nil {
			widget.Image(c, "img", f.img, w, o)
		} else {
			doc, sh := f.doc, c.Shaper()
			widget.VectorImage(c, "img", widget.Vector{Size: size, Fill: th.C(tImageViewBg), Draw: func(fr render.Frame, r render.Rect) { doc.Draw(fr, r, sh) }}, w, o)
		}
		c.Text("info", fmt.Sprintf("%d × %d · %s · %s", int(math.Round(float64(size.X))), int(math.Round(float64(size.Y))), kit.Bytes(f.size),
			strings.ToUpper(f.format)), kit.MonoFont(11, th.C(theme.FgFaint)))
	})
}

// card is a file that isn't drawn here: its name, what it is and its size, why, and a button that opens it with the OS.
func (x *Images) card(c *ui.Ctx, path string, f *viewedFile) {
	th := c.Theme()
	kind := fileKind(path, "")
	why := "Oriel doesn't show " + kind + "s."
	switch {
	case os.IsNotExist(f.err):
		why = "The file isn't there any more."
	case f.err != nil && kit.IsImage(path):
		why = "It can't be read as an image: " + f.err.Error()
	case strings.Contains(kind, "video") || strings.Contains(kind, "audio"):
		why = "Oriel doesn't play " + mediaKinds[strings.ToLower(filepath.Ext(path))] + "."
	}
	c.Column("card", ui.Style{W: ui.Size{Max: 460}, Pad: ui.Pad(24), Gap: 10, Cross: ui.Center, Radius: 8, Stroke: th.C(theme.LineStrong), Border: 1,
		Fill: th.C(theme.BgSurface), Role: ui.RoleGroup, Label: filepath.Base(path)}, func(c *ui.Ctx) {
		c.Text("glyph", mediaGlyph(kind), kit.UIFont(30, th.C(theme.FgFaint)))
		c.Text("name", filepath.Base(path), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 15, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		meta := kind
		if f.err == nil || !os.IsNotExist(f.err) {
			meta += " · " + kit.Bytes(f.size)
		}
		c.Text("meta", meta, kit.MonoFont(11, th.C(theme.FgMuted)))
		c.Text("why", why, kit.UIFont(12.5, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{H: ui.Px(4)}, nil)
		if widget.Button(c, "open", "Open in default app", widget.ButtonOpts{Primary: true, Help: imageHelp + "#card"}) {
			if err := x.h.Run(context.Background(), "system.open", registry.Args{"path": path}); err != nil {
				x.h.Log().Warn("image", "err", err)
			}
		}
	})
}

func mediaGlyph(kind string) string {
	switch {
	case strings.Contains(kind, "video"):
		return "▶"
	case strings.Contains(kind, "audio"):
		return "♪"
	case strings.Contains(kind, "image"):
		return "▨"
	}
	return "▤"
}

type imageView struct{ x *Images }

func (v imageView) Title(instance string) string { return filepath.Base(instance) }
func (v imageView) EdgeToEdge()                  {}
func (v imageView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "image", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

func (v imageView) Persist(instance string) (json.RawMessage, bool) {
	b, _ := json.Marshal(instance)
	return b, true
}

func (v imageView) Revive(state json.RawMessage) (string, error) {
	var p string
	if err := json.Unmarshal(state, &p); err != nil {
		return "", err
	}
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}
