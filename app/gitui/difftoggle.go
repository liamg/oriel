package gitui

import (
	"errors"
	"path/filepath"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/services"
)

// toggleFile goes from the focused diff to its selected line in the editor, or from the editor's file to its diff.
func (x *Diff) toggleFile(ctx registry.Ctx) error {
	if st := x.focus; st != nil && st.focused {
		if path, line, _, ok := st.selLine(); ok {
			return x.h.Run(ctx, "editor.open", registry.Args{"path": path, "line": line})
		}
		if st.sel.file == "" {
			return errors.New("no file of the diff is selected")
		}
		return x.h.Run(ctx, "editor.open", registry.Args{"path": filepath.Join(st.root, filepath.FromSlash(st.sel.file))})
	}
	ed, ok := services.EditorsOf(x.h)
	if !ok {
		return errors.New("no editor")
	}
	b, ok := ed.Current()
	if !ok || b.Path == "" {
		return errors.New("the editor has no file open")
	}
	return x.open(ctx, b.Path, b.Cursor.Line+1, false, false)
}
