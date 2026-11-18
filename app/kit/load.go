package kit

import (
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
)

// LoadState is a load as the toolkit shows it.
func LoadState(l registry.Load) widget.LoadState {
	switch {
	case l.Done:
		return widget.LoadState{Phase: widget.Ready}
	case l.Err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: l.Err}
	}
	return widget.LoadState{Phase: widget.Pending, Since: l.Since}
}
