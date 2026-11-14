package script

import (
	"errors"
	"path/filepath"

	"github.com/liamg/oriel/core"
)

// Traced is an error that knows where it happened and how it got there: a Lua error.
type Traced interface {
	Where() string
	Trace() string
}

// WithTrace gives a notice for err its location and trace, and an action titled title that opens the line. A
// relative location is under dir.
func WithTrace(n core.Notice, err error, dir, title string) core.Notice {
	var t Traced
	if !errors.As(err, &t) {
		return n
	}
	n.Detail, n.Location = t.Trace(), t.Where()
	if file, line := SplitLoc(n.Location); line > 0 {
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		n.Actions = append([]core.Action{{Title: title, Command: "editor.open", Args: map[string]any{"path": file, "line": line}}}, n.Actions...)
	}
	return n
}
