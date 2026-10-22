package kit

import (
	"fmt"
	"path/filepath"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// FilePreview is a picker's preview of a file's text, highlighted; it keeps the file it last showed.
type FilePreview struct {
	path string
	src  FileSrc
}

// Node is the file name (relative to dir) around line, 0-based; marked unless it's the first.
func (p *FilePreview) Node(r registry.Registry, dir, name string, line int) ui.Node {
	return &ui.Func{Key: "file", Fn: func(c *ui.Ctx) {
		path := filepath.Join(dir, name)
		if p.path != path {
			p.path, p.src = path, FileSrc{}
		}
		rd, _ := ReadsOf(r)
		src := p.src.Load(rd, BufferOn(r, path), path)
		if src.Missing() {
			c.Text("err", fmt.Sprint(src.State().Err), ui.TextStyle{Color: c.Theme().C(theme.FgFaint)})
			return
		}
		widget.Async(c, "read", src.State(), widget.AsyncOpts{Label: "Reading " + name}, func(c *ui.Ctx) {
			f := src.Around(r, path, line+1, 3, 30)
			f.Mark, f.Title = line > 0, name
			f.Build(c, nil)
		})
	}}
}
