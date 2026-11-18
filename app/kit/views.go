package kit

import (
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// The window's views, which a plugin may replace.
const (
	RootView       = "app.root"        // fills the window
	StatusLineView = "app.status-line" // along the bottom
	// TitleBarView is the bar across the top: the logo, the session tabs, and segments named title-bar.right.* (the
	// usage and bus indicators) at its right.
	TitleBarView = "app.title-bar"
	// The status line's segments are views named for their side and place: status-line.left.00-mode,
	// status-line.right.90-position. Any plugin adds one by registering a view so named; they sit in name order. A
	// segment with nothing to show builds no node, and takes no room.
	StatusLeft  = "status-line.left."
	StatusRight = "status-line.right."
	// RailView is the project sidebar down the window's left edge: each project's header and, under the
	// open one, its panels; then the session's entries. Wide, or narrow as an icon strip.
	RailView = "app.rail"
	// EditorView is the editor pane's view id; each window is an instance of it, named by the window's id.
	EditorView = "editor"
	// OverviewView is the project overview, which the window shows with a directory or session open but no view in it.
	OverviewView = "app.overview"
	// OverviewPanelsView is a directory's overview panels alone, the directory its instance: the Repos page's stats.
	OverviewPanelsView = "app.overview-panels"
	// AgentConsoleView is a session's agent console.
	AgentConsoleView = "agent-console"
	// ConsoleSide prefixes the views a plugin adds to the agent console's right column.
	ConsoleSide = "agent-console.side."
	// RequestsView is the Requests list: every thread of the session in front, by where it stands.
	RequestsView = "requests"
	// RequestView is a thread's own tab: the request, its comments and the reply box.
	RequestView = "request"
	// TicketView is a ticket's own tab, its key the instance: its title, state, fields and description in full.
	TicketView = "ticket"
	// PermissionsView is what the session in front's sandboxed agent may do: the asks waiting on you, each gateway
	// tool and network rule in force with where it comes from, and every connection it made or tried.
	PermissionsView = "agent-permissions"
	// ExplorerView is the Explorer: the work tree in three scopes, Changed, Uncommitted and All.
	ExplorerView = "explorer"
	// DiffView is a repo's branch diff: one instance per work tree.
	DiffView = "diff"
	// PlanDocsView is a planning session's plan documents.
	PlanDocsView = "plan-docs"
	// SearchView is project search.
	SearchView = "search"
	// TerminalView is a terminal: one instance per terminal.
	TerminalView = "terminal"
	// ReplaceView is a change to files shown as a diff before it's made, by Apply (edits.preview): a replace, a rename.
	ReplaceView = "search-replace"
)

// ViewSlots are the slots a view fills by its id: the slot's name, a dot, then the view's own name
// (status-line.left.00-mode, app.overlay.omnibox). Any other view id is a name alone. Names are kebab-case.
var ViewSlots = []string{"app", "app.overlay", "status-line.left", "status-line.right", "title-bar.right", "agent-console.side",
	"diff.header", "diff.side", "diff.right"}

// DiffHeader is the prefix of the views a diff's header shows at its right.
const DiffHeader = "diff.header."

// WindowNow is the window's clock, a test's fixed one; before there's a window, the registry's, the same clock. A
// zero time would date a wait begun then from long ago: its loading state would show at once and keep the result
// back for its least time on screen, on a clock a test only moves on purpose.
func WindowNow(h plugin.Host) time.Time {
	if u := h.UI(); u != nil {
		return u.Now()
	}
	return h.Clock().Now()
}

// The diff's view prefixes: DiffSide views beside the changed files (authorship), DiffRight views in a column at the
// right (an AI review's findings).
const (
	DiffSide  = "diff.side."
	DiffRight = "diff.right."
)

// SidebarArgs are a command's args as the sidebar runs it: a view docked on the left opens alone and fixed, so the
// dock shows one panel at a time.
func SidebarArgs(command string, a registry.Args) registry.Args {
	if !strings.HasPrefix(command, "view.") || a.String("place") != "dock" || a.String("edge") != "left" {
		return a
	}
	a = maps.Clone(a)
	a["alone"], a["fixed"] = true, true
	return a
}

// ThemeNames are the registered themes' names, sorted.
func ThemeNames(h registry.Registry) []string {
	srcs := h.Themes()
	var out []string
	for _, src := range srcs {
		if !slices.Contains(out, src.Name()) {
			out = append(out, src.Name())
		}
	}
	slices.Sort(out)
	return out
}
