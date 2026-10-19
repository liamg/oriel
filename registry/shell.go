package registry

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// RailEntry is an entry in the project sidebar down the window's left edge (ui.md 01h): one of each project's panels,
// listed under the open project, or one of the session's, below them. It runs Command with Args, a project entry
// with the project too ("project" in its args), and is dimmed while the command wouldn't run.
type RailEntry struct {
	Name string // unique among rail entries: "explorer"
	Icon string // a built-in icon's name or a glyph
	// Draw draws the icon itself, in place of Icon, as a size-pixel square in tone: a live one, as the assistant's.
	Draw    func(c *ui.Ctx, key any, size float32, tone render.Color)
	Title   string // its label, and its tooltip with the key bound to Command and Args
	Command string
	Args    Args
	Scope   string     // ScopeProjectEntry, ScopeSessionEntry or ScopeGlobalEntry; "" is the session's
	Badge   func() int // a count shown as a pill; nil or 0 for none
	// Note is dim text at the entry's right, for a project ("" for a session or global entry): "4", "48 ✓", "$18.40 today".
	Note func(project string) string
	// Active says the entry's panel is showing, for a project ("" for a session entry).
	Active func(project string) bool
	// Shown says whether the entry is listed at all, for a project ("" for a session entry); nil always is.
	Shown  func(project string) bool
	Order  int  // top to bottom
	Bottom bool // last, as Settings is
	Source string
}

// Rail entry scopes.
const (
	ScopeProjectEntry = "project"
	ScopeSessionEntry = "session"
	ScopeGlobalEntry  = "global" // pages of every session's, below a second divider: Stats, Usage, Settings
)

// ArgsFor is the args the entry runs its command with for project: a project entry's name it, unless project is ""
// (the window's only one).
func (e RailEntry) ArgsFor(project string) Args {
	if e.Scope != ScopeProjectEntry || project == "" {
		return e.Args
	}
	a := Args{}
	for k, v := range e.Args {
		a[k] = v
	}
	a["project"] = project
	return a
}

// Is says whether the entry is listed for project, asking its Shown.
func (e RailEntry) Is(project string) bool {
	if e.Shown == nil {
		return true
	}
	ok := false
	Call(func() error { ok = e.Shown(project); return nil })
	return ok
}

// ProjectState adds to a project's line on its sidebar header, after its branch and changes (ui.md 01h): a PR, a CI
// result. State runs on the app's loop as the sidebar is drawn, so it answers from what it has.
type ProjectState struct {
	Name   string
	Order  int
	State  func(project, dir string) []StatePart
	Source string
}

// StatePart is one piece of a project's state: its text in Style (a theme token, "" for the line's own), and a dot
// before it in the same colour, as a CI result has. The worst part's dot marks the project in the narrow sidebar.
type StatePart struct {
	Text  string
	Style string
	Dot   bool
}

func (ProjectState) ContributionKind() string   { return "project.state" }
func (p ProjectState) ContributionName() string { return p.Name }

// ProjectStateParts are every ProjectState's parts for a project, by Order; one that panics gives none.
func ProjectStateParts(r Registry, project, dir string) []StatePart {
	var ss []ProjectState
	for _, c := range r.Contributions("project.state") {
		ss = append(ss, c.(ProjectState))
	}
	slices.SortFunc(ss, func(a, b ProjectState) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	var out []StatePart
	for _, s := range ss {
		if s.State == nil {
			continue
		}
		var ps []StatePart
		Call(func() error { ps = s.State(project, dir); return nil })
		out = append(out, ps...)
	}
	return out
}

// StartEntry is an action on the start view (ui.md 01e), dimmed while its command isn't available. A Link is a small
// link under the actions instead of a card, with its Note: a page and its headline figure.
type StartEntry struct {
	Name    string // unique among start entries: "session.new"
	Icon    string
	Title   string
	Desc    string
	Command string
	Args    Args
	Order   int
	Link    bool
	Note    func() string // a link's figure: "$96.40 this week"
	Shown   func() bool   // whether it's listed at all; nil always is
	Source  string
}

func (RailEntry) ContributionKind() string    { return "rail.entry" }
func (e RailEntry) ContributionName() string  { return e.Name }
func (StartEntry) ContributionKind() string   { return "start.entry" }
func (e StartEntry) ContributionName() string { return e.Name }

// RailEntries are the rail's entries, top ones then bottom ones, each by Order.
func RailEntries(r Registry) []RailEntry {
	var out []RailEntry
	for _, c := range r.Contributions("rail.entry") {
		out = append(out, c.(RailEntry))
	}
	slices.SortFunc(out, func(a, b RailEntry) int {
		if a.Bottom != b.Bottom {
			if a.Bottom {
				return 1
			}
			return -1
		}
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// ProjectEntries are the entries listed under each project, by Order.
func ProjectEntries(r Registry) []RailEntry {
	return slices.DeleteFunc(RailEntries(r), func(e RailEntry) bool { return e.Scope != ScopeProjectEntry })
}

// SessionEntries are the session's entries, below the projects: top ones, then the bottom ones, each by Order.
func SessionEntries(r Registry) []RailEntry {
	return slices.DeleteFunc(RailEntries(r), func(e RailEntry) bool { return e.Scope == ScopeProjectEntry || e.Scope == ScopeGlobalEntry })
}

// GlobalEntries are the entries for every session, listed last: top ones, then the bottom ones, each by Order.
func GlobalEntries(r Registry) []RailEntry {
	return slices.DeleteFunc(RailEntries(r), func(e RailEntry) bool { return e.Scope != ScopeGlobalEntry })
}

// Is says whether the entry is listed, asking its Shown.
func (e StartEntry) Is() bool {
	if e.Shown == nil {
		return true
	}
	ok := false
	Call(func() error { ok = e.Shown(); return nil })
	return ok
}

// StartEntries are the start view's actions that are shown, by Order.
func StartEntries(r Registry) []StartEntry {
	var out []StartEntry
	for _, c := range r.Contributions("start.entry") {
		if e := c.(StartEntry); e.Is() {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b StartEntry) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// OverviewPanel is a panel on each card of the project overview (ui.md 01g): one card per repo or directory open.
// Compute runs off the app's loop, once per card and again when a repo's HEAD moves; the node it returns is drawn in
// the card, under Title.
type OverviewPanel struct {
	Name    string // unique among panels: "languages"
	Title   string
	Order   int
	Width   float32 // 0 shares the width the fixed ones leave
	Repos   bool    // on git repos' cards
	Dirs    bool    // on plain directories' cards
	Compute func(ctx Ctx, dir string) (ui.Node, error)
	Source  string
}

func (OverviewPanel) ContributionKind() string   { return "overview.panel" }
func (p OverviewPanel) ContributionName() string { return p.Name }

// OverviewPanels are the overview's panels by Order.
func OverviewPanels(r Registry) []OverviewPanel {
	var out []OverviewPanel
	for _, c := range r.Contributions("overview.panel") {
		out = append(out, c.(OverviewPanel))
	}
	slices.SortFunc(out, func(a, b OverviewPanel) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// KeyLayer is a keymap mode laid over every other while Active says so: a popup's keys, as the completion menu's
// <C-n> and <CR>. A key the layer doesn't map goes on as it would without it.
type KeyLayer struct {
	Name   string // unique among layers: "completion"
	Mode   string // the keymap mode its keys are in
	Active func() bool
	Source string
}

func (KeyLayer) ContributionKind() string   { return "key.layer" }
func (l KeyLayer) ContributionName() string { return l.Name }

// ActiveKeyModes are the modes of the layers active now, by name.
func ActiveKeyModes(r Registry) []string {
	var out []string
	for _, c := range r.Contributions("key.layer") {
		l := c.(KeyLayer)
		on := false
		Call(func() error { on = l.Active != nil && l.Active(); return nil })
		if on {
			out = append(out, l.Mode)
		}
	}
	return out
}

// CmdlineHint is text shown at the right of the editor's : line while it applies: the matches a :s has, or a better
// way to do what's typed.
type CmdlineHint struct {
	Name   string
	Hint   func(line string) string // "" for none
	Source string
}

func (CmdlineHint) ContributionKind() string   { return "cmdline.hint" }
func (h CmdlineHint) ContributionName() string { return h.Name }

// CmdlineHints are the hints that apply to line, by name.
func CmdlineHints(r Registry, line string) []string {
	var out []string
	for _, c := range r.Contributions("cmdline.hint") {
		h := c.(CmdlineHint)
		s := ""
		Call(func() error { s = h.Hint(line); return nil })
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ExclusiveKeys is a key mode that stands alone while Active says so (nil: always): keys resolving in it, the modes
// after it ("n", "global") lend it only the keymaps Allow lets through (nil: none), and none of their longer keys, so
// a screen keeps to its own keys (the start view to its entries'). Key layers before it are untouched.
type ExclusiveKeys struct {
	Mode   string
	Active func() bool
	Allow  func(Keymap) bool
	Source string
}

func (ExclusiveKeys) ContributionKind() string   { return "keys.exclusive" }
func (e ExclusiveKeys) ContributionName() string { return e.Mode }

// RawKeys is a key mode whose widget takes control keys itself: while it has focus, global maps of ⌃ keys (without ⌘
// or ⌥) don't apply, so a terminal's program gets ⌃W and ⌃J. Its own mode's maps still do.
type RawKeys struct {
	Mode   string
	Source string
}

func (RawKeys) ContributionKind() string   { return "keys.raw" }
func (r RawKeys) ContributionName() string { return r.Mode }

// IsRawKeyMode reports whether mode takes control keys itself.
func IsRawKeyMode(r Registry, mode string) bool {
	for _, c := range r.Contributions("keys.raw") {
		if c.(RawKeys).Mode == mode {
			return true
		}
	}
	return false
}

// SettingsSection is a section of the settings page that isn't options (settings.md): listed with the options'
// sections by Name, its rows drawn by Build.
type SettingsSection struct {
	Name   string     // "secrets"
	Count  func() int // the figure beside its name: how many are set; nil for none
	Build  func(c *ui.Ctx)
	Source string
}

func (SettingsSection) ContributionKind() string   { return "settings.section" }
func (s SettingsSection) ContributionName() string { return s.Name }

// SettingsSections are the settings page's own sections, by name.
func SettingsSections(r Registry) []SettingsSection {
	var out []SettingsSection
	for _, c := range r.Contributions("settings.section") {
		out = append(out, c.(SettingsSection))
	}
	slices.SortFunc(out, func(a, b SettingsSection) int { return strings.Compare(a.Name, b.Name) })
	return out
}
