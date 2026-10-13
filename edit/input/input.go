// Package input is the pluggable input engine: what turns keys into the editing primitives of package edit.
// Vim is one engine, plain (CUA keys) another, and a plugin can register more, emacs for example (editor.md).
package input

import "github.com/liamg/oriel/edit"

// Engine is an input engine. Attach gives it a window's editor and returns the engine's state for that window.
type Engine interface {
	ID() string // "vim", "plain", "emacs"
	Attach(ed *edit.Editor) State
}

// State is an engine's state in one window. Keys come in vim notation, one per key press: "x", "<C-x>", "<D-s>".
type State interface {
	Key(k string) Result // the hot path: once per key
	Mode() Mode
	Pending() string // the keys of a command not finished yet, for which-key: "d2", "C-x"
}

// Result is what a key did. Command, when set, is a registry command for the IDE to run: a save, a palette.
type Result struct {
	Handled bool
	Command string
	Args    map[string]any
}

// Shape is how the cursor looks.
type Shape int

const (
	Block Shape = iota
	Bar
	Underline
)

// Mode is what the status line shows of an engine's state: its label ("NORMAL", "EMACS"), the cursor's shape,
// and the keymap mode the IDE looks keys up in first, so plugins can map keys in it.
type Mode struct {
	Label   string
	Shape   Shape
	KeyMode string
}

// Wrap is an engine that sees each key before inner does, and decides whether to hand it on with next.
func Wrap(inner Engine, id string, fn func(k string, next func(string) Result) Result) Engine {
	return wrapped{inner, id, fn}
}

type wrapped struct {
	inner Engine
	id    string
	fn    func(string, func(string) Result) Result
}

func (w wrapped) ID() string { return w.id }

func (w wrapped) Attach(ed *edit.Editor) State {
	return wrappedState{w.inner.Attach(ed), w.fn}
}

type wrappedState struct {
	State
	fn func(string, func(string) Result) Result
}

func (s wrappedState) Key(k string) Result { return s.fn(k, s.State.Key) }
