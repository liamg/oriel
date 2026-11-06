package kit

import (
	"path/filepath"

	"github.com/liamg/oriel/core"
)

// AnchorPath is where an anchor's file is.
func AnchorPath(se core.SessionState, a *core.Anchor) string {
	if filepath.IsAbs(a.File) {
		return a.File
	}
	if a.Project == core.DocsProject {
		return filepath.Join(se.Dir, filepath.FromSlash(a.File))
	}
	p, _ := se.Primary()
	for _, q := range se.Projects {
		if q.Name == a.Project && a.Project != "" {
			p = q
		}
	}
	return filepath.Join(p.Dir, filepath.FromSlash(a.File))
}
