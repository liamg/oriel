package kit

import (
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// The window's views, which a plugin may replace.
const (
	RootView       = "app.root"       // fills the window
	StatusLineView = "app.statusline" // along the bottom
	// TitleBarView is the bar across the top: the logo, the session tabs, and segments named titlebar.right.* (the
	// usage and bus indicators) at its right.
	TitleBarView = "app.titlebar"
	// The status line's segments are views named for their side and place: statusline.left.00-mode,
	// statusline.right.90-position. Any plugin adds one by registering a view so named; they sit in name order. A
	// segment with nothing to show builds no node, and takes no room.
	StatusLeft  = "statusline.left."
	StatusRight = "statusline.right."
	// RailView is the project sidebar down the window's left edge: each project's header and, under the
	// open one, its panels; then the session's entries. Wide, or narrow as an icon strip.
	RailView = "app.rail"
	// EditorView is the editor pane's view id; each window is an instance of it, named by the window's id.
	EditorView = "editor"
	// OverviewPanelsView is a directory's overview panels alone, the directory its instance: the Repos page's stats.
	OverviewPanelsView = "app.overview.panels"
	// AgentConsoleView is a session's agent console.
	AgentConsoleView = "agent.console"
	// ConsoleSide prefixes the views a plugin adds to the agent console's right column.
	ConsoleSide = "agent.console.side."
	// RequestsView is the Requests list: every thread of the session in front, by where it stands.
	RequestsView = "requests"
	// RequestView is a thread's own tab: the request, its comments and the reply box.
	RequestView = "request"
	// PermissionsView is what the session in front's sandboxed agent may do: the asks waiting on you, each gateway
	// tool and network rule in force with where it comes from, and every connection it made or tried.
	PermissionsView = "agent.permissions"
	// ExplorerView is the Explorer: the work tree in three scopes, Changed, Uncommitted and All.
	ExplorerView = "explorer"
	// DiffView is a repo's branch diff: one instance per work tree.
	DiffView = "diff"
	// PlanDocsView is a planning session's plan documents.
	PlanDocsView = "plan.docs"
	// SearchView is project search.
	SearchView = "search"
	// TerminalView is a terminal: one instance per terminal.
	TerminalView = "terminal"
	// ReplaceView is a change to files shown as a diff before it's made, by Apply (edits.preview): a replace, a rename.
	ReplaceView = "search.replace"
)

// Traced is an error that knows where it happened and how it got there: a Lua error.
type Traced interface {
	Where() string
	Trace() string
}

// WithTrace gives a notice for err its location and trace, and an action titled title that opens the line. A
// relative location is a config file's.
func WithTrace(n core.Notice, err error, dir, title string) core.Notice {
	var t Traced
	if !errors.As(err, &t) {
		return n
	}
	n.Detail, n.Location = t.Trace(), t.Where()
	if file, line := script.SplitLoc(n.Location); line > 0 {
		if !filepath.IsAbs(file) {
			file = filepath.Join(dir, file)
		}
		n.Actions = append([]core.Action{{Title: title, Command: "editor.open", Args: map[string]any{"path": file, "line": line}}}, n.Actions...)
	}
	return n
}

// DiffHeader is the prefix of the views a diff's header shows at its right.
const DiffHeader = "diff.header."

// WindowNow is the window's clock, a test's fixed one; zero before there's a window, so a wait begun then shows at
// once.
func WindowNow(h plugin.Host) time.Time {
	if u := h.UI(); u != nil {
		return u.Now()
	}
	return time.Time{}
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
