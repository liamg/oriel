package gitui

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
)

// diffLineSubject is what a diff's line stands for to a right-click menu.
const diffLineSubject = "diff.line"

// lineMenu is a diff line's right-click menu; the right-click selects the line, so each acts on it.
func (x *Diff) lineMenu() []registry.MenuItem {
	return []registry.MenuItem{
		{Name: "diff.line.source", Label: "Open in file", Key: "o", Group: "file", Order: 10, Command: "diff.source"},
		{Name: "diff.line.comment", Label: "Comment…", Key: "gc", Group: "thread", Order: 20, Command: "diff.comment"},
		{Name: "diff.line.ask", Label: "Ask the agent…", Key: "ga", Group: "thread", Order: 21, Command: "diff.comment", Args: registry.Args{"to": "agent"}},
		{Name: "diff.line.explain", Label: "Explain", Key: "?", Group: "thread", Order: 22, Command: "diff.explain"},
		{Name: "diff.line.copy", Label: "Copy reference", Group: "edit", Order: 30, Run: func(registry.Ctx, registry.MenuCtx) error {
			st := x.focus
			if st == nil {
				return errors.New("no diff is open")
			}
			path, line, _, ok := st.selLine()
			if !ok {
				return errors.New("no line is selected")
			}
			if rel, err := filepath.Rel(st.root, path); err == nil {
				path = rel
			}
			kit.CopyText(x.h, fmt.Sprintf("%s:%d", filepath.ToSlash(path), line))
			return nil
		}},
	}
}
