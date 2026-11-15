package hostapi

import (
	"time"

	"github.com/liamg/oriel/core"
)

// Trust is whose GitHub text the agent may read (crit's trusted orgs): members of Orgs and the Users named. With
// neither set, the repository's owner is the trusted org.
type Trust struct {
	Orgs  []string `json:"orgs,omitempty"`
	Users []string `json:"users,omitempty"`
}

// MirrorReq syncs a session's PRs' reviews; Fresh asks GitHub for the PR again rather than the last 30 s's.
type MirrorReq struct {
	Session core.SessionID `json:"session"`
	Fresh   bool           `json:"fresh,omitempty"`
	Trust   Trust          `json:"trust,omitzero"`
}

// Mirror is what a sync found.
type Mirror struct {
	Synced time.Time  `json:"synced"`
	PRs    []MirrorPR `json:"prs,omitempty"`
	Errors []string   `json:"errors,omitempty"`
}

// MirrorPR is one project's PR and where each reviewer stands (gh.ReviewStates).
type MirrorPR struct {
	Project string            `json:"project"`
	Repo    string            `json:"repo"`
	Number  int               `json:"number"`
	URL     string            `json:"url,omitempty"`
	Reviews map[string]string `json:"reviews,omitempty"`
	Head    string            `json:"head,omitempty"` // a reviewed PR's head commit on GitHub
	Open    bool              `json:"open,omitempty"` // not closed or merged
}

// ForwardReq forwards an agent's draft reply on a GitHub thread to GitHub.
type ForwardReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
}

// DraftEditReq rewrites a draft reply before it's forwarded.
type DraftEditReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
	Body    string         `json:"body"`
}

// PRSeenReq is what a code host last said of a project's branch: whether its pull request is open.
type PRSeenReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Branch  string         `json:"branch"`
	Open    bool           `json:"open,omitempty"`
}

// Ship is a project on its way to a merged pull request.
type Ship struct {
	Project   string       `json:"project"`
	Branch    string       `json:"branch,omitempty"`
	Base      string       `json:"base,omitempty"`      // the branch it merges into: main
	Default   string       `json:"default,omitempty"`   // the repo's default branch, when it has it locally: main
	HasOrigin bool         `json:"hasOrigin,omitempty"` // it has an origin to push to
	Origin    string       `json:"origin,omitempty"`    // origin's URL
	Remotes   []string     `json:"remotes,omitempty"`   // the repo's remotes, when none is origin
	ReadOnly  bool         `json:"readOnly,omitempty"`  // a pinned or read-only worktree, with nothing to ship
	Files     []ShipFile   `json:"files,omitempty"`     // uncommitted
	Behind    int          `json:"behind,omitempty"`    // commits on the base it lacks
	BaseAhead int          `json:"baseAhead,omitempty"` // commits it has from the local base that origin's lacks: a PR would show them again
	Conflicts []string     `json:"conflicts,omitempty"`
	Commits   int          `json:"commits,omitempty"` // on the branch since its base
	Unpushed  []ShipCommit `json:"unpushed,omitempty"`
	First     ShipCommit   `json:"first,omitzero"` // the branch's own first commit, pushed or not
	Pushed    bool         `json:"pushed,omitempty"`
	Head      string       `json:"head,omitempty"`     // the commit checked out
	Replaced  int          `json:"replaced,omitempty"` // commits on origin's branch a rebase rewrote: the next push replaces them
	Synced    time.Time    `json:"synced,omitzero"`    // when it was last rebased onto its base from here
	// CommitDraft is the commit message drafted for it (Body), PRDraft the pull request's.
	CommitDraft  Draft    `json:"commitDraft,omitzero"`
	PRDraft      Draft    `json:"prDraft,omitzero"`
	Template     string   `json:"template,omitempty"` // the repo's pull request template
	TemplateFile string   `json:"templateFile,omitempty"`
	Owners       []string `json:"owners,omitempty"` // CODEOWNERS of what the branch changes
	GitError     string   `json:"gitError,omitempty"`
	// Off is set while the worktree is on a branch other than the session's for it, which Oriel won't push.
	Off *OffBranch `json:"off,omitempty"`
}

// ShipFile is an uncommitted file.
type ShipFile struct {
	Path   string `json:"path"`
	Code   string `json:"code"` // git's letter: M, A, D, R, ? (untracked), U (conflicted)
	Add    int    `json:"add,omitempty"`
	Del    int    `json:"del,omitempty"`
	Binary bool   `json:"binary,omitempty"`
}

// ShipCommit is a commit on the branch.
type ShipCommit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Body    string `json:"body,omitempty"`
	Author  string `json:"author,omitempty"`
}

// ShipReq reads a session's projects; Fresh fetches first.
type ShipReq struct {
	Session  core.SessionID `json:"session"`
	Fresh    bool           `json:"fresh,omitempty"`
	Projects []string       `json:"projects,omitempty"` // only these projects, in the session's order; every one when empty
}

// ShipRef is one project of a session.
type ShipRef struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
}

// ShipCommitReq commits files of a project (every change when none) with a message.
type ShipCommitReq struct {
	Session  core.SessionID `json:"session"`
	Project  string         `json:"project"`
	Message  string         `json:"message"`
	Files    []string       `json:"files,omitempty"`    // none commits every change, untracked files too
	Trailers []string       `json:"trailers,omitempty"` // added to the message, as git commit --trailer does
}

// DraftReq saves a project's commit message (Kind "commit": Body) or PR draft (Kind "pr") as the user wrote it, or
// with Drop clears it, so the agent drafts it again.
type DraftReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title,omitempty"`
	Body    string         `json:"body"`
	Drop    bool           `json:"drop,omitempty"`
}

// RenameReq renames a project's branch.
type RenameReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Name    string         `json:"name"`
}

// ResolveReq resolves a thread mirrored from GitHub there too, or reopens it, as the user did in Oriel.
type ResolveReq struct {
	Session  core.SessionID `json:"session"`
	Thread   core.ThreadID  `json:"thread"`
	Resolved bool           `json:"resolved"`
}

// MirrorReviewReq brings one PR's review from a code host other than GitHub into a session: Threads as the app read
// them through the host's plugin. A thread with no Key is the PR's conversation.
type MirrorReviewReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Host    string         `json:"host"` // the code host's name
	Repo    string         `json:"repo"`
	PR      int            `json:"pr"`
	URL     string         `json:"url,omitempty"`
	Author  string         `json:"author,omitempty"` // the PR's: in a review, what others wrote isn't the agent's to answer
	Threads []ReviewThread `json:"threads"`
}

// ReviewThread is a review thread as its code host has it.
type ReviewThread struct {
	Key      string          `json:"key,omitempty"`
	Path     string          `json:"path,omitempty"`
	Line     int             `json:"line,omitempty"`
	Old      bool            `json:"old,omitempty"` // on the old side of the diff
	Outdated bool            `json:"outdated,omitempty"`
	Resolved *bool           `json:"resolved,omitempty"`
	Reviewer string          `json:"reviewer,omitempty"`
	State    string          `json:"state,omitempty"`
	URL      string          `json:"url,omitempty"`
	Comments []ReviewComment `json:"comments"`
}

// ReviewComment is one comment of a ReviewThread; Trusted says the agent may read it, Mine that the user wrote it.
// Written is its text as written where the host changed it as it was posted.
type ReviewComment struct {
	Key     string    `json:"key"`
	User    string    `json:"user"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
	Trusted bool      `json:"trusted,omitempty"`
	Mine    bool      `json:"mine,omitempty"`
	Written string    `json:"written,omitempty"`
}

// ForwardedReq marks a draft reply sent: posted on its thread's code host as HostID.
type ForwardedReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
	HostID  string         `json:"hostId"`
}
