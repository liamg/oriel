package registry

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/dap"
)

// DebugAdapter is a debug adapter (DB-1): the languages it debugs, how to start and reach it, and the launch
// arguments its configs start from. It has dap.AdapterSpec's fields, so each converts to the other; the dap module
// stays free of the registry.
type DebugAdapter struct {
	Name      string // unique among adapters: "dlv"
	Type      string // what a launch config's type names: "go"
	Language  []string
	Cmd       []string // ${addr} and ${port} as dap.AdapterSpec says
	Transport string   // stdio, tcp or reverse
	Probe     []string
	Launch    map[string]any
	Attach    map[string]any
	Infer     func(file string) (dap.LaunchConfig, bool)
	Order     int
	Source    string
	Connect   dap.Launcher // reaches the adapter some other way than running Cmd: in process, over a socket
}

func (DebugAdapter) ContributionKind() string   { return "dap.adapter" }
func (a DebugAdapter) ContributionName() string { return a.Name }

// DebugAdapters are the registered debug adapters by Order, then name.
func DebugAdapters(r Registry) []DebugAdapter {
	var out []DebugAdapter
	for _, c := range r.Contributions("dap.adapter") {
		out = append(out, c.(DebugAdapter))
	}
	slices.SortFunc(out, func(a, b DebugAdapter) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// GutterAction is what a click on an editor's line numbers does in the files it applies to: runs Command with Args
// and the file and the 1-based line clicked ({file, line}). The breakpoint toggle is one.
type GutterAction struct {
	Name    string
	Command string
	Args    Args
	When    func(path string) bool // nil: every file
	Order   int                    // the lowest that applies wins
	Source  string
}

func (GutterAction) ContributionKind() string   { return "editor.gutter" }
func (a GutterAction) ContributionName() string { return a.Name }

// GutterActionFor is the gutter action for a file, if any applies.
func GutterActionFor(r Registry, path string) (GutterAction, bool) {
	var out []GutterAction
	for _, c := range r.Contributions("editor.gutter") {
		a := c.(GutterAction)
		ok := a.When == nil
		if !ok {
			Call(func() error { ok = a.When(path); return nil })
		}
		if ok {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return GutterAction{}, false
	}
	slices.SortFunc(out, func(a, b GutterAction) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out[0], true
}
