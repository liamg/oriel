package kit

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// PRView is Raise PR / Edit PR, a main tab; its instance is the project.
const PRView = "pr"

// ErrStopped is a step a hook stopped; its reason is shown where the step was asked for.
var ErrStopped = errors.New("stopped by a hook")

// ShortSHA is a commit's first seven characters.
func ShortSHA(s string) string { return s[:min(len(s), 7)] }

// The ship and review pages' header, side panel and section captions.
var (
	TShipHead  = theme.Declare("ship.header.bg", "bg.surface")
	TShipSide  = theme.Declare("ship.side.bg", "bg.surface")
	TShipCapFg = theme.Declare("ship.caption", "fg.faint")
)

// SectionCap is a section's caption: "CHECKS · 5 OF 7".
func SectionCap(c *ui.Ctx, s string) { c.Text(s, s, KickerFont(c.Theme().C(TShipCapFg))) }

// EmptyNote says why there's nothing to show.
func EmptyNote(c *ui.Ctx, msg string) {
	c.Column("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 14)}, func(c *ui.Ctx) {
		widget.Wrap(c, "t", msg, 280, UIFont(12, c.Theme().C(theme.FgFaint)))
	})
}

// PRLabel is a PR as a chip says it: "#4418".
func PRLabel(pr string) string {
	if i := strings.LastIndex(pr, "/"); i >= 0 && i < len(pr)-1 {
		return "#" + pr[i+1:]
	}
	return pr
}

// PrPushed is the PrPushed hook's event: a session's pull request's head, each time GitHub is read for it; the
// subject is the session.
type PrPushed struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Head    string `json:"head"`
}

const EndHelp = "shell/tabs#end"

const ArchiveOnMergeOption = "session.archive_on_merge"

// AllMerged is whether every project with something to ship has its PR merged, and one at least has. A project
// that moved on from a merged PR with nothing since counts as merged.
func AllMerged(se core.SessionState, ships []registry.ProjectShip) bool {
	merged := 0
	for _, s := range ships {
		switch {
		case s.ReadOnly:
		case s.PR != nil && s.PR.Merged, s.PR == nil && s.Commits == 0 && movedOnMerged(se, s.Project):
			merged++
		case s.PR != nil || len(s.Files) > 0 || s.Unpushed > 0:
			return false
		}
	}
	return merged > 0
}

// movedOnMerged says the last PR a project moved on from was merged.
func movedOnMerged(se core.SessionState, project string) bool {
	for _, pr := range slices.Backward(se.Past) {
		if pr.Project == project {
			return pr.State == "merged"
		}
	}
	return false
}

// ArchivesOnMerge says session.archive_on_merge archives se as it stands: it's on, every PR is merged and nothing
// is Uncommitted.
func ArchivesOnMerge(h plugin.Host, se core.SessionState, ships []registry.ProjectShip) bool {
	primary, _ := se.Primary()
	on, _ := registry.Option[bool](h.Options(), ArchiveOnMergeOption, registry.At{Project: primary.Dir, Session: string(se.ID)})
	return on && !se.Archived && AllMerged(se, ships) && Uncommitted(ships) == 0
}

// Uncommitted counts the files ships have uncommitted.
func Uncommitted(ships []registry.ProjectShip) int {
	n := 0
	for _, s := range ships {
		n += len(s.Files)
	}
	return n
}

// The views.
const (
	ShipView        = "ship"         // the session's page: every project side by side
	PullRequestView = "pull-request" // a project's compact checklist, in the dock
	GitView         = "git"          // a project's branch, changes and commit box, in the dock
)

// PrSeen is the PrSeen hook's event: a session's pull request, the first time the app reads it, whoever raised it
// (the Ship page, the agent, gh by hand).
type PrSeen struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Ticket  string `json:"ticket,omitempty"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Draft   bool   `json:"draft,omitempty"`
	Merged  bool   `json:"merged,omitempty"`
}

// PrMerged is the PrMerged hook's event: a session's pull request read as merged, the first time the app sees it so,
// whoever merged it. Open is how many of the session's other PRs are still open, or not known.
type PrMerged struct {
	Session string `json:"session"`
	Project string `json:"project"`
	Ticket  string `json:"ticket,omitempty"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Open    int    `json:"open"`
}

// WordsOf are how h names a pull request; "PR" with no host.
func WordsOf(h *registry.CodeHost) registry.Words {
	if h == nil {
		return registry.CodeHost{}.Said()
	}
	return h.Said()
}

// RunOrNotify runs cmd from a click or a key, and says why it failed in a notice from source: one row per source,
// stamped with the window's clock. A hook's stop (ErrStopped) is shown where the step was asked for, so it's none.
// It returns the error, for a page that also shows it inline.
func RunOrNotify(h plugin.Host, source, cmd string, a registry.Args) error {
	err := h.Run(context.Background(), cmd, a)
	if err != nil && !errors.Is(err, ErrStopped) {
		h.Log().Warn(source, "command", cmd, "err", err)
		h.Notify(core.Notice{ID: source + ".error", Severity: core.SevError, Source: source, Key: true, Text: err.Error()})
	}
	return err
}
