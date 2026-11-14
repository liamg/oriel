package settings

import (
	"image"

	"github.com/liamg/oriel/theme"
)

// What the package's outside tests read of its plugins.

func (x *About) Open() bool { return x.open }

const LogoBlink = logoBlink

func (e *ThemeEditor) Picking() bool  { return e.picking }
func (e *ThemeEditor) Open() bool     { return e.open }
func (e *ThemeEditor) Hover() string  { return e.hover }
func (e *ThemeEditor) Picked() string { return e.picked }

// Probe is the window as pick mode took it, and the probe theme it was drawn in.
func (e *ThemeEditor) Probe() (*image.RGBA, *theme.Resolved) { return e.probe, e.probeTh }

const TypingPause = settingsTypingPause

func (x *Prompts) Selected() string { return x.sel }

// SetFilter filters the Plugins page's list, as typing in its filter does.
func (x *Catalog) SetFilter(s string) { x.page.filter = s }
