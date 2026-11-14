package hostapi

import (
	"path"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// RepoCheck is what a path would add, as the Add repo dialog shows it while it's typed.
type RepoCheck struct {
	Path     string      `json:"path"`               // the checkout it adds: a worktree's main clone
	Worktree bool        `json:"worktree,omitempty"` // the path is a worktree of Path
	Branch   string      `json:"branch,omitempty"`
	Remote   string      `json:"remote,omitempty"`
	Known    core.RepoID `json:"known,omitempty"` // already registered as this
	Error    string      `json:"error,omitempty"`
}

// CloneReq is repos.clone: a URL, the folder its checkout goes in, and its name there.
type CloneReq struct {
	URL    string `json:"url"`
	Parent string `json:"parent,omitempty"` // CloneParent if ""
	Name   string `json:"name,omitempty"`   // the URL's last part if ""
	// Progress has git's progress lines as the clone runs.
	Progress func(line string) `json:"-"`
}

// CloneTarget is where a clone would go, and what's there now.
type CloneTarget struct {
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	Exists bool   `json:"exists,omitempty"` // something is at Dir
	Same   bool   `json:"same,omitempty"`   // it's a clone of the URL: it's added as it is
}

// RepoFacts are a checkout's numbers, as the Repos page shows them.
type RepoFacts struct {
	Branch    string        `json:"branch"`
	Default   string        `json:"default,omitempty"`
	Tracking  gitx.Tracking `json:"tracking,omitzero"`
	Branches  int           `json:"branches"`
	Worktrees int           `json:"worktrees"` // besides the checkout itself
	Tags      int           `json:"tags"`
	LatestTag string        `json:"latestTag,omitempty"`
	Last      gitx.Commit   `json:"last,omitzero"`
	SizeKiB   int64         `json:"sizeKiB"`           // the object store
	Signing   bool          `json:"signing,omitempty"` // a signing key is set up
}

// StaleBranch is a local branch housekeeping offers to delete.
type StaleBranch struct {
	Name     string    `json:"name"`
	Why      string    `json:"why"` // WhyMerged or WhyGone
	When     time.Time `json:"when,omitzero"`
	Worktree string    `json:"worktree,omitempty"` // a worktree that has it checked out
	Dirty    bool      `json:"dirty,omitempty"`    // its worktree has uncommitted changes
	Session  string    `json:"session,omitempty"`  // a live session works on it: it's shown, not offered
}

// HousekeepPlan is what housekeeping would clean in a checkout.
type HousekeepPlan struct {
	Branches []StaleBranch `json:"branches,omitempty"`
	Stale    []string      `json:"stale,omitempty"` // worktrees whose directory is gone
	Base     string        `json:"base"`            // what "merged" is merged into
}

// HousekeepReq is repos.housekeep: with no Apply, the plan; with it, those branches deleted.
type HousekeepReq struct {
	Repo  core.RepoID `json:"repo"`
	Fetch bool        `json:"fetch,omitempty"` // fetch --prune first, so "upstream gone" is true
	Apply []string    `json:"apply,omitempty"` // branches to delete
	Force []string    `json:"force,omitempty"` // of those, the ones whose dirty worktree goes anyway
	Prune bool        `json:"prune,omitempty"` // git worktree prune
}

// Cleaned is what happened to one branch.
type Cleaned struct {
	Branch string `json:"branch"`
	Error  string `json:"error,omitempty"`
}

// TagPreview is what the New tag form starts from.
type TagPreview struct {
	Branches []string `json:"branches"`
	Default  string   `json:"default"`
	Latest   string   `json:"latest,omitempty"` // the highest semver tag
	Commit   string   `json:"commit,omitempty"` // what the branch points at now
	Subject  string   `json:"subject,omitempty"`
	Signing  bool     `json:"signing,omitempty"`
	Remote   bool     `json:"remote,omitempty"`
}

// TagReq is repos.tag: an annotated tag on a branch, pulled first, signed and pushed if asked.
type TagReq struct {
	Repo    core.RepoID `json:"repo"`
	Branch  string      `json:"branch,omitempty"` // the default branch if ""
	Name    string      `json:"name"`
	Message string      `json:"message"`
	Pull    bool        `json:"pull,omitempty"`
	Sign    bool        `json:"sign,omitempty"`
	Push    bool        `json:"push,omitempty"`
}

// Tagged is a tag made: the commit, and how its push went. A failed push keeps the tag.
type Tagged struct {
	Name    string `json:"name"`
	Commit  string `json:"commit"`
	Subject string `json:"subject,omitempty"`
	Pushed  bool   `json:"pushed,omitempty"`
	PushErr string `json:"pushErr,omitempty"`
}

// Why a branch is up for cleaning.
const (
	WhyMerged = "merged"
	WhyGone   = "upstream gone"
)

// CloneName is the folder a clone of url is named by default: its last part, with .git dropped.
func CloneName(url string) string {
	return Sanitise(strings.TrimSuffix(path.Base(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(url), ":", "/"), "/")), ".git"))
}
