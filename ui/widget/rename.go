package widget

import (
	"strings"

	"github.com/liamg/oriel/ui"
)

// Rename is a name being edited in place: a tab's, a card's. Start it with the name as it is.
type Rename struct {
	On    bool
	Name  string
	focus bool
}

// Start edits name; the field takes focus when next drawn.
func (r *Rename) Start(name string) { r.On, r.Name, r.focus = true, name, true }

// RenameField is r's field, its text selected: ⏎ ends it and returns the name typed, trimmed (done, with "" for
// none); Escape ends it with nothing.
func RenameField(c *ui.Ctx, key any, r *Rename, o InputOpts) (name string, done bool) {
	if r.focus {
		c.RequestFocus(c.Next(key))
		r.focus = false
	}
	o.SelectOnFocus = true
	res := Input(c, key, &r.Name, o)
	switch {
	case res.Submitted:
		r.On = false
		return strings.TrimSpace(r.Name), true
	case res.Cancelled:
		r.On = false
	}
	return "", false
}
