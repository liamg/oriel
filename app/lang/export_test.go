package lang

import (
	"os"
	"slices"
	"strings"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/ui/widget"
)

// What the package's outside tests read and set of its plugins.

func (x *LSP) CompletionOpen() bool   { return x.comp.open }
func (x *LSP) Clients() []*lsp.Client { return x.m.Clients() }
func (x *LSP) AddToHover(md string)   { x.hover.md.Text += md }

// ForgetDocs forgets every buffer's server document, as if no file had opened on a server yet.
func (x *LSP) ForgetDocs() {
	x.mu.Lock()
	clear(x.docs)
	x.mu.Unlock()
}

func (x *Tests) History() *testrun.History { return x.history }
func (x *Tests) Rescan(path string)        { x.rescan(path) }
func (x *Tests) Watching() bool            { return x.watch }

// Tree is the Tests panel's tree, as its filter has it; TreeAll is every test's.
func (x *Tests) Tree() []widget.TreeItem {
	known, _ := x.known()
	return x.treeItems(known, x.latest(), x.panel.filter)
}

func (x *Tests) TreeAll() []widget.TreeItem {
	known, _ := x.known()
	return x.treeItems(known, x.latest(), 0)
}

// Breakpoint is a breakpoint as the debugger keeps it.
type Breakpoint = debugBreakpoint

// ConsoleLine is a line of the debug console.
type ConsoleLine = consoleLine

func (x *Debug) Breakpoints(file string) []Breakpoint { return x.bps[file] }
func (x *Debug) Console() []ConsoleLine               { return x.console }
func (x *Debug) Watches() []string                    { return x.watches }
func (x *Debug) WatchValues() int                     { return len(x.panel.watchVals) }
func (x *Debug) Counts() string                       { return x.counts() }
func (x *Debug) Configs() ([]dap.LaunchConfig, error) { return x.configs() }
func (x *Debug) StartNamed(name string) error         { return x.startNamed(name) }
func (x *Debug) StepLevel() string                    { return x.level(debugStep) }

// Reload forgets the breakpoints and watches, and reads them from where they're kept.
func (x *Debug) Reload() {
	x.state.Wait()
	x.bps, x.watches = nil, nil
	b, _ := os.ReadFile(x.state.Path())
	x.load(b)
}

// ReplaceInConsole replaces old with new in the console's lines.
func (x *Debug) ReplaceInConsole(old, new string) {
	for i, l := range x.console {
		x.console[i].Text = strings.ReplaceAll(l.Text, old, new)
	}
}

// PausedAt says the session is paused in a frame named name on a 0-based line, its variables in.
func (x *Debug) PausedAt(name string, line int) bool {
	s := x.sess
	if s == nil {
		return false
	}
	f, ok := s.top()
	return ok && f.Name == name && f.Line == line && len(s.scopes) > 0 && s.vars[s.scopes[0].Ref] != nil
}

// Paused says the session is paused, and how many times it's stopped.
func (x *Debug) Paused() (bool, int) {
	if x.sess == nil {
		return false, 0
	}
	return x.sess.state == debugPaused, x.sess.gen
}

// Ended says the session has ended.
func (x *Debug) Ended() bool { return x.sess != nil && x.sess.state == debugEnded }

// Launched is the session's launch config.
func (x *Debug) Launched() dap.LaunchConfig { return x.sess.cfg }

// HasVariable says a variable read in the session evaluates as expr.
func (x *Debug) HasVariable(expr string) bool {
	for _, vs := range x.sess.vars {
		if slices.ContainsFunc(vs, func(v dap.Variable) bool { return v.Evaluate == expr }) {
			return true
		}
	}
	return false
}

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

// Scans counts the whole scans of the window's directory for its tests.
func (x *Tests) Scans() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.scans
}
