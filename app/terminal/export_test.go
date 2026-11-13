package terminal

import "github.com/liamg/oriel/term"

// The terminal with instance id's commands, its exit dots by row, its tab's title, its terminal-normal line, and
// whether it has a selection.
func (x *Terminals) Commands(id string) []term.Command { return x.get(id).emu.Commands() }
func (x *Terminals) ExitDots(id string) map[int]string { return x.get(id).exitDots() }
func (x *Terminals) Title(id string) string            { return x.get(id).title() }
func (x *Terminals) NormalLine(id string) int          { return x.normalLine(x.get(id)) }
func (x *Terminals) Selected(id string) bool {
	_, _, ok := x.get(id).selection()
	return ok
}

// Terminal is the terminal of instance id, as any: the same one the window comes back to, or another.
func (x *Terminals) Terminal(id string) any {
	if t := x.get(id); t != nil {
		return t
	}
	return nil
}
