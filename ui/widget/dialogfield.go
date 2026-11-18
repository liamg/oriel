package widget

import (
	"github.com/liamg/oriel/ui"
)

// DialogField is a dialog's text field: Escape calls cancel and Enter submit (either may be nil). The dialog's first
// field sets o.DefaultFocus; one that must take the focus later asks for it once (Ctx.RequestFocus) where that
// happens.
func DialogField(c *ui.Ctx, key any, text *string, o InputOpts, cancel, submit func()) {
	switch r := Input(c, key, text, o); {
	case r.Cancelled && cancel != nil:
		cancel()
	case r.Submitted && submit != nil:
		submit()
	}
}
