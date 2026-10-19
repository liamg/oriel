package app

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

// ImageView is the view a file the image plugin claims opens in: its instance is the file's absolute path.
const ImageView = "image"

const imageHelp = "files/image"

var tImageViewBg = theme.Declare("image.view.bg", "bg.base")

// imagePlugin is the built-in file viewer for what isn't text: images drawn fit to their tab, and other binary files
// (video, audio, PDFs) as a card that opens them with the OS. It's written as a plugin would be.
type imagePlugin struct {
	disposer
	h     plugin.Host
	files map[string]*viewedFile
}

// viewedFile is a file as its tab last read it; it's read again a second after.
type viewedFile struct {
	img    image.Image
	format string
	err    error
	size   int64
	read   time.Time
}

func (x *imagePlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "image"} }

func (x *imagePlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.files = h, map[string]*viewedFile{}
	x.add(h.View(ImageView, imageView{x}))
	x.add(h.Contribute(registry.FileViewer{Name: "image", When: isMedia, View: ImageView, Order: 50, Help: imageHelp, Source: "builtin:image"}))
	x.add(disposeFn(h.Layout().OnClose(func(v layout.ViewRef) {
		if v.ID == ImageView {
			delete(x.files, v.Instance)
		}
	})))
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
	if isImage(path) || mediaKinds[strings.ToLower(filepath.Ext(path))] != "" {
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
	return isBinary(b[:n])
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

// file is path as last read, read again when a second has passed: its tab follows the file on disk.
func (x *imagePlugin) file(c *ui.Ctx, path string) *viewedFile {
	now := c.Now()
	f := x.files[path]
	if f == nil || now.Sub(f.read) >= time.Second {
		f = &viewedFile{read: now}
		if fi, err := os.Stat(path); err != nil {
			f.err = err
		} else {
			f.size = fi.Size()
			if isImage(path) {
				f.img, f.format, f.err = pictures.file(path)
			}
		}
		x.files[path] = f
	}
	c.WakeAt(f.read.Add(time.Second))
	return f
}

func (x *imagePlugin) view(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	path := vc.Instance
	f := x.file(c, path)
	name := filepath.Base(path)
	c.Column("image", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(24), Gap: 12, Main: ui.Center, Cross: ui.Center, Fill: th.C(tImageViewBg),
		Clip: true, Role: ui.RoleRegion, Label: name, Help: imageHelp}, func(c *ui.Ctx) {
		if f.img == nil {
			x.card(c, path, f)
			return
		}
		b := f.img.Bounds()
		availW, availH := max(vc.Width-48, 16), max(vc.Height-48-12-16, 16)
		w := min(float32(b.Dx()), availW, availH*float32(b.Dx())/float32(max(b.Dy(), 1)))
		widget.Image(c, "img", f.img, w, widget.ImageOpts{Label: name, Help: imageHelp, Zoom: true, Group: "file:" + path})
		c.Text("info", fmt.Sprintf("%d × %d · %s · %s", b.Dx(), b.Dy(), humanBytes(f.size), strings.ToUpper(f.format)), monoFont(11, th.C(theme.FgFaint)))
	})
}

// card is a file that isn't drawn here: its name, what it is and its size, why, and a button that opens it with the OS.
func (x *imagePlugin) card(c *ui.Ctx, path string, f *viewedFile) {
	th := c.Theme()
	kind := fileKind(path, "")
	why := "Oriel doesn't show " + kind + "s."
	switch {
	case os.IsNotExist(f.err):
		why = "The file isn't there any more."
	case f.err == errSVG:
		why = "SVG isn't drawn here: only raster images are."
	case f.err != nil && isImage(path):
		why = "It can't be read as an image: " + f.err.Error()
	case strings.Contains(kind, "video") || strings.Contains(kind, "audio"):
		why = "Oriel doesn't play " + mediaKinds[strings.ToLower(filepath.Ext(path))] + "."
	}
	c.Column("card", ui.Style{W: ui.Size{Max: 460}, Pad: ui.Pad(24), Gap: 10, Cross: ui.Center, Radius: 8, Stroke: th.C(theme.LineStrong), Border: 1,
		Fill: th.C(theme.BgSurface), Role: ui.RoleGroup, Label: filepath.Base(path)}, func(c *ui.Ctx) {
		c.Text("glyph", mediaGlyph(kind), uiFont(30, th.C(theme.FgFaint)))
		c.Text("name", filepath.Base(path), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 15, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		meta := kind
		if f.err == nil || !os.IsNotExist(f.err) {
			meta += " · " + humanBytes(f.size)
		}
		c.Text("meta", meta, monoFont(11, th.C(theme.FgMuted)))
		c.Text("why", why, uiFont(12.5, th.C(theme.FgFaint)))
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

type imageView struct{ x *imagePlugin }

func (v imageView) Title(instance string) string { return filepath.Base(instance) }
func (v imageView) EdgeToEdge()                  {}
func (v imageView) Build(vc registry.ViewCtx) ui.Node {
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
