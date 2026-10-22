package uireg

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// RailEntry is an entry in the project sidebar down the window's left edge: one of each project's panels,
// listed under the open project, or one of the session's, below them. It runs Command with Args, a project entry
// with the project too ("project" in its args), and is dimmed while the command wouldn't run.
type RailEntry struct {
	Name string // unique among rail entries: "explorer"
	Icon string // a built-in icon's name or a glyph
	// Draw draws the icon itself, in place of Icon, as a size-pixel square in tone: a live one, as the assistant's.
	Draw    func(c *ui.Ctx, key any, size float32, tone render.Color)
	Title   string // its label, and its tooltip with the key bound to Command and Args
	Command string
	Args    registry.Args
	Scope   string     // ScopeProjectEntry, ScopeSessionEntry or ScopeGlobalEntry; "" is the session's
	Badge   func() int // a count shown as a pill; nil or 0 for none
	// BadgeTone is the pill's colour, a theme token; "" is rail.badge.
	BadgeTone string
	// Note is dim text at the entry's right, for a project ("" for a session or global entry): "4", "48 ✓", "$18.40 today".
	Note func(project string) string
	// Active says the entry's panel is showing, for a project ("" for a session entry).
	Active func(project string) bool
	// Shown says whether the entry is listed at all, for a project ("" for a session entry); nil always is.
	Shown func(project string) bool
	// Kinds are the kinds of session it's listed on, and When what their kind must be able to do; any, with none.
	Kinds  []core.Kind
	When   registry.Capability
	Order  int  // top to bottom
	Bottom bool // last, as Settings is
	Source string
}

// Rail entry scopes.
const (
	ScopeProjectEntry = "project"
	ScopeSessionEntry = "session"
	ScopeGlobalEntry  = "global" // pages of every session's, at the sidebar's foot: Stats, Usage, Settings
)

// ArgsFor is the args the entry runs its command with for project: a project entry's name it, unless project is ""
// (the window's only one).
func (e RailEntry) ArgsFor(project string) registry.Args {
	if e.Scope != ScopeProjectEntry || project == "" {
		return e.Args
	}
	a := registry.Args{}
	for k, v := range e.Args {
		a[k] = v
	}
	a["project"] = project
	return a
}

// For says whether the entry is listed on a session of kind k (Kinds, When); k is "" for a window on a directory with
// no session, which lists the entries with no Kinds.
func (e RailEntry) For(r registry.Contributions, k core.Kind) bool {
	if k == "" {
		return len(e.Kinds) == 0
	}
	return registry.ForSession(r, k, e.Kinds, e.When)
}

// Is says whether the entry is listed for project, asking its Shown.
func (e RailEntry) Is(project string) bool {
	if e.Shown == nil {
		return true
	}
	ok := false
	registry.Call(func() error { ok = e.Shown(project); return nil })
	return ok
}

// ProjectState adds to a project's line on its sidebar header, after its branch and changes: a PR, a CI
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
func ProjectStateParts(r registry.Contributions, project, dir string) []StatePart {
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
		registry.Call(func() error { ps = s.State(project, dir); return nil })
		out = append(out, ps...)
	}
	return out
}

// StartEntry is an action on the start view, dimmed while its command isn't available. A Link is a small
// link under the actions instead of a card, with its Note: a page and its headline figure.
type StartEntry struct {
	Name    string // unique among start entries: "session.new"
	Icon    string
	Title   string
	Desc    string
	Command string
	Args    registry.Args
	Order   int
	Link    bool
	// Unlisted isn't drawn: only its command's keys work on the start view, which keeps the others out.
	Unlisted bool
	Note     func() string // a link's figure: "$96.40 this week"
	Shown    func() bool   // whether it's listed at all; nil always is
	Source   string
}

func (RailEntry) ContributionKind() string    { return "rail.entry" }
func (e RailEntry) ContributionName() string  { return e.Name }
func (StartEntry) ContributionKind() string   { return "start.entry" }
func (e StartEntry) ContributionName() string { return e.Name }

// RailEntries are the rail's entries, top ones then bottom ones, each by Order.
func RailEntries(r registry.Contributions) []RailEntry {
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
func ProjectEntries(r registry.Contributions) []RailEntry {
	return slices.DeleteFunc(RailEntries(r), func(e RailEntry) bool { return e.Scope != ScopeProjectEntry })
}

// SessionEntries are the session's entries, below the projects: top ones, then the bottom ones, each by Order.
func SessionEntries(r registry.Contributions) []RailEntry {
	return slices.DeleteFunc(RailEntries(r), func(e RailEntry) bool { return e.Scope == ScopeProjectEntry || e.Scope == ScopeGlobalEntry })
}

// GlobalEntries are the entries for every session, listed last: top ones, then the bottom ones, each by Order.
func GlobalEntries(r registry.Contributions) []RailEntry {
	return slices.DeleteFunc(RailEntries(r), func(e RailEntry) bool { return e.Scope != ScopeGlobalEntry })
}

// Is says whether the entry is listed, asking its Shown.
func (e StartEntry) Is() bool {
	if e.Shown == nil {
		return true
	}
	ok := false
	registry.Call(func() error { ok = e.Shown(); return nil })
	return ok
}

// StartEntries are the start view's actions that are shown, by Order.
func StartEntries(r registry.Contributions) []StartEntry {
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

// Place is a full-window page the window's history goes back and forward through (nav.back, nav.forward, the mouse's
// side buttons). The history is the session in front and the place showing, recorded whenever either changes.
type Place struct {
	Name    string      // unique among places: "stats"
	Title   string      // what the history calls it
	Shown   func() bool // it's showing now
	Command string      // opens it
	Args    registry.Args
	Close   func() // puts it away
	Source  string
}

func (Place) ContributionKind() string   { return "place" }
func (p Place) ContributionName() string { return p.Name }

// PlaceByName is the place called name.
func PlaceByName(r registry.Contributions, name string) (Place, bool) {
	for _, c := range r.Contributions("place") {
		if p := c.(Place); p.Name == name {
			return p, true
		}
	}
	return Place{}, false
}

// ShownPlace is the place showing now, "" for none; ok is false while two are, as one opens over another.
func ShownPlace(r registry.Contributions) (name string, ok bool) {
	for _, c := range r.Contributions("place") {
		p := c.(Place)
		shown := false
		if p.Shown != nil {
			registry.Call(func() error { shown = p.Shown(); return nil })
		}
		if !shown {
			continue
		}
		if name != "" {
			return "", false
		}
		name = p.Name
	}
	return name, true
}

// OverviewPanel is a panel on each card of the project overview: one card per repo or directory open.
// Compute runs off the app's loop, once per card and again when a repo's HEAD moves; the node it returns is drawn in
// the card, under Title.
type OverviewPanel struct {
	Name    string // unique among panels: "languages"
	Title   string
	Order   int
	Width   float32 // 0 shares the width the fixed ones leave
	Repos   bool    // on git repos' cards
	Dirs    bool    // on plain directories' cards
	Compute func(ctx registry.Ctx, dir string) (ui.Node, error)
	Source  string
}

func (OverviewPanel) ContributionKind() string   { return "overview.panel" }
func (p OverviewPanel) ContributionName() string { return p.Name }

// OverviewPanels are the overview's panels by Order.
func OverviewPanels(r registry.Contributions) []OverviewPanel {
	var out []OverviewPanel
	for _, c := range r.Contributions("overview.panel") {
		out = append(out, c.(OverviewPanel))
	}
	slices.SortFunc(out, func(a, b OverviewPanel) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// SettingsSection is a section of the settings page that isn't options: listed with the options'
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
func SettingsSections(r registry.Contributions) []SettingsSection {
	var out []SettingsSection
	for _, c := range r.Contributions("settings.section") {
		out = append(out, c.(SettingsSection))
	}
	slices.SortFunc(out, func(a, b SettingsSection) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Indicator is a small live readout in a slot: the title bar's right, the status line's sides, the
// HUD's panels, a panel's own slot. What Build returns is kept and drawn until the indicator is built again: every
// Every, when one of the events named in On happens, or when it's refreshed (the hud.refresh command). A node that
// reads state as it's drawn (a ui.Func) is live without any of them.
type Indicator struct {
	Name  string // unique among indicators: "deploys"
	Slot  string // SlotTitleBar, SlotTitleBrand, SlotStatusLeft, SlotStatusRight, SlotHUD, SlotMission, or a slot a panel draws: SlotHUDToday, SlotInboxFoot
	Order int    // in its slot, among the slot's views: titlebar.right.40-usage is 40
	// Title is a HUD panel's heading, Note what it says at the heading's right ("3 online"), and Span how many of the
	// HUD's four columns it takes (1 if 0). Help is its help page and anchor.
	Title, Help string
	Note        func() string
	Span        int
	Every       time.Duration
	On          []string // core event names that build it again: "MachineChanged"
	Build       func(ctx registry.Ctx) (ui.Node, error)
	Global      bool // it doesn't need a session: a Global frame (Home) draws it too
	Source      string
}

// The slots indicators go in.
const (
	SlotTitleBar    = "titlebar.right"
	SlotTitleBrand  = "titlebar.brand" // the title bar's left, before the session tabs: the logo
	SlotStatusLeft  = "statusline.left"
	SlotStatusRight = "statusline.right"
	SlotHUD         = "hud"        // the HUD's panels
	SlotHUDToday    = "hud.today"  // under the HUD's Today panel: a plugin's lines
	SlotMission     = "mission"    // mission control's strip along its foot: usage, workers' load
	SlotInboxFoot   = "inbox.foot" // the Inbox panel's foot: agentbus's presence
)

// Indicators are the indicators in slot, by Order then name.
func Indicators(r registry.Contributions, slot string) []Indicator {
	var out []Indicator
	for _, c := range r.Contributions("hud.indicator") {
		if i := c.(Indicator); i.Slot == slot {
			out = append(out, i)
		}
	}
	slices.SortStableFunc(out, func(a, b Indicator) int { return cmp.Compare(a.Order, b.Order) })
	return out
}

func (Indicator) ContributionKind() string   { return "hud.indicator" }
func (t Indicator) ContributionName() string { return t.Name }
