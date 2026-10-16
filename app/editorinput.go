package app

import (
	"strings"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/edit/input/plain"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/vim"
)

// InputOption picks the input engine, for every buffer or one: "vim" (the default), "plain", or one a plugin
// registered.
const InputOption = "editor.input"

// vimInput is the vim engine as an input.Engine. The editor keeps one vim engine for all its windows, since vim's
// windows, buffers and registers are shared; each window's state is that engine with the window current.
type vimInput struct{ x *editorPlugin }

func (vimInput) ID() string                        { return "vim" }
func (v vimInput) Attach(*edit.Editor) input.State { return vimState(v) }

type vimState struct{ x *editorPlugin }

func (s vimState) Key(k string) input.Result {
	s.x.e.Key(k)
	return input.Result{Handled: true}
}

func (s vimState) Mode() input.Mode {
	m := s.x.e.Mode()
	shape := map[vim.Shape]input.Shape{vim.Block: input.Block, vim.Bar: input.Bar, vim.Underline: input.Underline}[s.x.e.CursorShape()]
	return input.Mode{Label: modeName(m), Shape: shape, KeyMode: keyMode(m), Text: m == "insert" || m == "replace"}
}

func (s vimState) Pending() string { return strings.Join(s.x.e.Pending(), "") }

// builtinInputs registers the engines Oriel ships, and the input option.
func (x *editorPlugin) builtinInputs() {
	x.add(x.h.Input(vimInput{x}))
	x.add(x.h.Input(plain.Engine{}))
	x.add(x.h.Option(registry.OptionSpec{Name: InputOption, Type: registry.String, Default: "vim", Choices: x.h.Inputs,
		Scopes: registry.ScopeUser | registry.ScopeProject | registry.ScopeBuffer,
		Help:   "The input engine: vim, plain (CUA keys), or one a plugin adds. Set per buffer to change one buffer's."}))
}

// paneInput is the engine for window id's buffer, attached to the window: the same state while the window keeps
// the same engine, a fresh one when the option changes.
type paneInput struct {
	engine string
	state  input.State
}

func (x *editorPlugin) inputFor(p vim.Pane) input.State {
	id := "vim"
	if v, _, err := x.h.Options().Get(InputOption, registry.At{Buffer: p.Name()}); err == nil {
		id, _ = v.(string)
	}
	if id == "vim" {
		return vimState{x}
	}
	eng, ok := x.h.InputByID(id)
	if !ok {
		return vimState{x}
	}
	cur := x.inputs[p.ID()]
	if cur.engine != id {
		cur = paneInput{engine: id, state: eng.Attach(p.Editor())}
		x.inputs[p.ID()] = cur
	}
	return cur.state
}

// isVim reports whether st is the vim engine, which the pane reads its cursor and selection from directly.
func isVim(st input.State) bool {
	_, ok := st.(vimState)
	return ok
}
