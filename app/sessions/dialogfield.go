package sessions

import (
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// dialogField is a dialog's text field: focused as the dialog opens when focus is set, Escape calling cancel and
// Enter submit (either may be nil).
func dialogField(c *ui.Ctx, key any, text *string, o widget.InputOpts, focus bool, cancel, submit func()) {
	if focus {
		c.RequestFocus(c.Next(key))
	}
	switch r := widget.Input(c, key, text, o); {
	case r.Cancelled && cancel != nil:
		cancel()
	case r.Submitted && submit != nil:
		submit()
	}
}
