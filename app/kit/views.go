package kit

import (
	"maps"
	"strings"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
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
	// OverviewView is the project overview, which the window shows with a directory or session open but no view in it.
	OverviewView = "app.overview"
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
	// TicketView is a ticket's own tab, its key the instance: its title, state, fields and description in full.
	TicketView = "ticket"
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

// Section is one of the side's blocks: a kicker with a note at its right, then body.
func Section(c *ui.Ctx, key, title, note string, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14), Gap: 9, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", strings.ToUpper(title), KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if note != "" {
				c.Text("n", note, MonoFont(9.5, th.C(theme.FgGhost)))
			}
		})
		body(c)
	})
	widget.Rule(c, key+".rule")
}

// Stat is a row of a side section: a name, and its value at the right in tone.
func Stat(c *ui.Ctx, key, name, value string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + value}, func(c *ui.Ctx) {
		c.Text("n", name, MonoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("v", value, MonoFont(11.5, tone))
	})
}

// StatAgo is a Stat whose value is how long ago at was, then suffix.
func StatAgo(c *ui.Ctx, key any, name string, at time.Time, suffix string, tone render.Color) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Role: ui.RoleText, Label: name + " " + widget.AgoLabel(c.Now(), at) + suffix}, func(c *ui.Ctx) {
		c.Text("n", name, MonoFont(11.5, th.C(theme.FgMuted)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		widget.Ago(c, "v", at, widget.AgoOpts{Text: MonoFont(11.5, tone), Suffix: suffix})
	})
}
