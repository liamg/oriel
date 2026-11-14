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

// Eval runs a line in the Lua REPL, as typing it there does.
func (x *Lua) Eval(line string) { x.eval(line) }

// Repl is the REPL's lines, and each one's kind: 0 input, 1 output, 2 error, 3 a frame of its trace.
func (x *Lua) Repl() (texts []string, kinds []int) {
	for _, l := range x.repl {
		texts, kinds = append(texts, l.text), append(kinds, l.kind)
	}
	return texts, kinds
}

func (x *Lua) Complete(line string) []string { _, names := x.complete(line); return names }
func (x *Lua) SetInput(s string)             { x.input = s }

// SetFilter filters the Plugins page's list, as typing in its filter does.
func (x *Catalog) SetFilter(s string) { x.page.filter = s }
