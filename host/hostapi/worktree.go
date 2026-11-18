package hostapi

import (
	"time"

	"github.com/liamg/oriel/core"
)

// PRMove is a review's project moved on to its pull request's new head: Commits on from From to To, one for a
// force-push that rewrote it.
type PRMove struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Commits int    `json:"commits"`
}

// NewBranchReq starts a fresh branch for a project: Name, else one made from the session's; Carry takes uncommitted
// work along, which is refused otherwise.
type NewBranchReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Name    string         `json:"name,omitempty"`
	Carry   bool           `json:"carry,omitempty"`
}

// NextPRReq moves one project of a session on from its PR: Done is that PR as its code host has it, and Head the
// commit it was at, whose commits count as the PR's even once it's squashed and its branch deleted.
// UpdatePRReq fetches a review session's pull request again (ShipCalls.UpdatePR).
type UpdatePRReq struct {
	Session core.SessionID `json:"session"`
}

type NextPRReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Done    core.PastPR    `json:"done,omitzero"`
	Head    string         `json:"head,omitempty"`
}

// Manifest is a workspace's record of itself. Its fields are crit's, so a crit workspace reads as one.
type Manifest struct {
	Name          string          `json:"name"`
	CreatedAt     time.Time       `json:"createdAt"`
	Projects      []ManifestEntry `json:"projects"`
	Kind          string          `json:"kind,omitempty"`
	Job           string          `json:"job,omitempty"`
	WorkStartedAt time.Time       `json:"workStartedAt,omitzero"`
	Ticket        string          `json:"ticket,omitempty"`
	TicketTitle   string          `json:"ticketTitle,omitempty"`
	TicketURL     string          `json:"ticketUrl,omitempty"`
	Model         string          `json:"model,omitempty"`
	Label         string          `json:"label,omitempty"`
}

// ManifestEntry is a project as the manifest records it: Dir relative to the workspace, so it can move.
type ManifestEntry struct {
	Name     string       `json:"name"`
	Repo     string       `json:"repo"`
	Dir      string       `json:"dir"`
	Branch   string       `json:"branch"`
	Primary  bool         `json:"primary"`
	Detached bool         `json:"detached,omitempty"`
	Commit   string       `json:"commit,omitempty"`
	Base     string       `json:"base,omitempty"`
	Past     []ManifestPR `json:"past,omitempty"`
}

type ManifestPR struct {
	Branch   string       `json:"branch"`
	Number   int          `json:"number"`
	URL      string       `json:"url"`
	Title    string       `json:"title"`
	State    core.PRState `json:"state"`
	ClosedAt time.Time    `json:"closedAt"`
}

// The workspace layout is crit's, unchanged: a directory holding the manifest, its own git repo
// for plan docs, and one worktree per project under repos/.
const (
	ManifestFile = ".oriel-workspace.json"
	ReposDir     = "repos"
)

// Templates a new repo can start from, besides any git URL (a GitHub template repo).
var Templates = []string{"empty", "go-module", "go-cli"}
