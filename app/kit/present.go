package kit

import (
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
)

// PresentKeyMode is a presentation's keys: j/k between blocks, c or ⏎ to comment on one, o to go to its source.
const PresentKeyMode = "present"

// PresenterService draws a buffer as a uireg.BufferPresenter builds it, the file's threads in its margin.
const PresenterService = "presenter"

// Presentation is a presenter drawing a buffer: in an editor window, or a view of its own (plan mode).
type Presentation struct {
	Presenter  uireg.BufferPresenter
	Path, Name string
	Buf        *text.Buffer
	Window     string    // the DecorationCtx's
	Kicker     string    // the document's label, for a presenter that draws a title
	Root       ui.ID     // focus anywhere in it is the presentation's
	Source     func(int) // go to a 0-based source line
}

// PresentState is a presentation's place: its selected block and its scroll.
type PresentState interface {
	// Focus gives the presentation focus next frame.
	Focus()
	// ScrollTo brings a 0-based source line to the top next frame.
	ScrollTo(line int)
}

// Presenter draws presentations, their keys (PresentKeyMode) working on whichever has focus.
type Presenter interface {
	// NewPresent is a presentation's place, at its top.
	NewPresent() PresentState
	// Present draws p in st's place, and reports the source line at its top and whether the reader scrolled it there.
	Present(c *ui.Ctx, h plugin.Host, st PresentState, p Presentation, width float32) (top int, scrolled bool)
}

func PresenterOf(r registry.Registry) (Presenter, bool) {
	return registry.ServiceOf[Presenter](r, PresenterService)
}
