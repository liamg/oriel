package registry

import (
	"cmp"
	"slices"
	"strings"
)

// FileViewer shows the files it claims in a view of its own instead of a buffer: an image, a PDF. editor.open opens a
// claimed file as a tab of View, its instance the file's absolute path.
type FileViewer struct {
	Name   string
	When   func(path string) bool // the files it shows, by absolute path
	View   string                 // the view a file opens in
	Order  int                    // the first by Order, then name, that claims a file shows it
	Help   string
	Source string
}

func (FileViewer) ContributionKind() string   { return "file.viewer" }
func (v FileViewer) ContributionName() string { return v.Name }

// ViewerFor is the viewer that shows path: the first by Order, then name, whose When claims it.
func ViewerFor(r Registry, path string) (FileViewer, bool) {
	var all []FileViewer
	for _, c := range r.Contributions("file.viewer") {
		all = append(all, c.(FileViewer))
	}
	slices.SortFunc(all, func(a, b FileViewer) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	for _, v := range all {
		ok := false
		if v.When != nil {
			Call(func() error { ok = v.When(path); return nil })
		}
		if ok && v.View != "" {
			return v, true
		}
	}
	return FileViewer{}, false
}
