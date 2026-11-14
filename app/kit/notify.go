package kit

import (
	"context"
	"errors"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// ErrStopped is a step a hook stopped; its reason is shown where the step was asked for.
var ErrStopped = errors.New("stopped by a hook")

// RunOrNotify runs cmd from a click or a key, and says why it failed in a notice from source: one row per source.
// It returns the error, for a page that also shows it inline.
func RunOrNotify(h plugin.Host, source, cmd string, a registry.Args) error {
	err := h.Run(context.Background(), cmd, a)
	NotifyError(h, source+".error", err, true)
	return err
}

// NotifyError says why something failed in a notice with id, and logs it; nil, or a hook's stop (ErrStopped, shown
// where the step was asked for), is none. autoClear is for a failure in answer to what the user just did, which they
// saw: its toast clears itself. Work that fails later, maybe while they look elsewhere, stays until it's dismissed.
func NotifyError(h plugin.Host, id string, err error, autoClear bool) {
	if err == nil || errors.Is(err, ErrStopped) {
		return
	}
	h.Log().Warn(id, "err", err)
	h.Notify(core.Notice{ID: id, Severity: core.SevError, Text: err.Error(), AutoClear: autoClear})
}
