package core

import (
	"strings"
	"time"
)

// ThreadID names a thread within its session.
type ThreadID string

// ThreadStatus is where a request is. A deferred thread doesn't hold up Start work.
type ThreadStatus string

const (
	ThreadOpen      ThreadStatus = "open"
	ThreadResolved  ThreadStatus = "resolved"
	ThreadDeferred  ThreadStatus = "deferred"
	ThreadDismissed ThreadStatus = "dismissed"
)

// Author is who wrote a comment.
type Author string

const (
	AuthorYou    Author = "you"
	AuthorAgent  Author = "agent"
	AuthorGitHub Author = "github"
)

// Anchor is the place a thread is about. A thread with no anchor is about the whole change.
type Anchor struct {
	Project string   `json:"project,omitempty"`
	File    string   `json:"file"` // relative to the project
	Line    int      `json:"line"` // 1-based
	EndLine int      `json:"endLine,omitempty"`
	Side    string   `json:"side,omitempty"` // "old" for removed code in a diff
	Text    string   `json:"text,omitempty"` // the anchored lines when it was made, so it can follow edits
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
}

// Activity is one thing the agent did on the way to a reply: an edit, a lookup, a test run.
type Activity struct {
	Kind    string `json:"kind"` // edit, read, search, lsp, test, shell, tool
	Title   string `json:"title"`
	Path    string `json:"path,omitempty"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Run     string `json:"run,omitempty"` // a TestRun's ID
	Failed  bool   `json:"failed,omitempty"`
	Item    string `json:"item,omitempty"` // the transcript item it came from
}

// Evidence backs a claim in a reply: "tests pass" links to the run that showed it.
type Evidence struct {
	Kind  string `json:"kind"` // test-run
	Ref   string `json:"ref"`
	Label string `json:"label"`
	OK    bool   `json:"ok"`
}

// Comment is one message in a thread.
type Comment struct {
	ID        string     `json:"id"`
	Author    Author     `json:"author"`
	Name      string     `json:"name,omitempty"` // a display name: the user's, the agent's provider, a GitHub login
	Body      string     `json:"body"`           // markdown
	Time      time.Time  `json:"time,omitzero"`
	Progress  bool       `json:"progress,omitempty"`  // an agent's "working on it" note, not an answer
	Btw       bool       `json:"btw,omitempty"`       // a side question, or its answer from a fork
	Delivered bool       `json:"delivered,omitempty"` // a comment of yours the agent has been given
	Images    []string   `json:"images,omitempty"`
	Model     string     `json:"model,omitempty"` // the agent's model and effort when it wrote this
	Severity  string     `json:"severity,omitempty"`
	Activity  []Activity `json:"activity,omitempty"`
	Evidence  []Evidence `json:"evidence,omitempty"`
	Unbacked  string     `json:"unbacked,omitempty"` // a claim with no evidence: "says tests pass, but ran none"
	// Draft is an agent's reply not sent where the thread came from: the user forwards it (a GitHub thread's).
	Draft bool `json:"draft,omitempty"`
	// GitHubID is the comment's on GitHub: one mirrored from there, or a draft once forwarded.
	GitHubID int64     `json:"githubId,omitempty"`
	Sent     time.Time `json:"sent,omitzero"`      // when the user forwarded it
	Redacted bool      `json:"redacted,omitempty"` // its author isn't trusted, so the agent isn't given its text
	Edited   bool      `json:"edited,omitempty"`   // changed where it came from since it was first mirrored
}

// Origins of threads that came from elsewhere.
const OriginGitHub = "github"

// GitHubThread is where a mirrored thread lives on GitHub: a review comment's thread on a PR's line, or the PR's
// conversation (Root 0).
type GitHubThread struct {
	Project  string `json:"project"`
	Repo     string `json:"repo"` // owner/name
	PR       int    `json:"pr"`
	Root     int64  `json:"root,omitempty"`     // the review thread's first comment
	Reviewer string `json:"reviewer,omitempty"` // who started it
	State    string `json:"state,omitempty"`    // the reviewer's review: CHANGES_REQUESTED, APPROVED, COMMENTED
	URL      string `json:"url,omitempty"`
	Resolved bool   `json:"resolved,omitempty"` // resolved on GitHub, as last synced
	Outdated bool   `json:"outdated,omitempty"` // its line is gone from the PR's head
}

// Touched is a file the agent changed while working on a thread.
type Touched struct {
	Project string `json:"project,omitempty"`
	Path    string `json:"path"`
	Added   int    `json:"added,omitempty"`
	Removed int    `json:"removed,omitempty"`
}

// Thread is a request: a comment and its replies, anchored to a place or to the whole change.
type Thread struct {
	ID       ThreadID      `json:"id"`
	Anchor   *Anchor       `json:"anchor,omitempty"`
	Title    string        `json:"title,omitempty"`
	Status   ThreadStatus  `json:"status"`
	Urgent   bool          `json:"urgent,omitempty"`
	Btw      bool          `json:"btw,omitempty"`    // a side question thread, answered by a fork
	Busy     bool          `json:"busy,omitempty"`   // the agent has it and hasn't answered
	Origin   string        `json:"origin,omitempty"` // where it came from: "" for Oriel, OriginGitHub
	GitHub   *GitHubThread `json:"github,omitempty"`
	Comments []Comment     `json:"comments"`
	Touched  []Touched     `json:"touched,omitempty"`
	Created  time.Time     `json:"created,omitzero"`
	Updated  time.Time     `json:"updated,omitzero"`
}

// Last is the last thing said in the thread.
func (t Thread) Last() (Comment, bool) {
	if len(t.Comments) == 0 {
		return Comment{}, false
	}
	return t.Comments[len(t.Comments)-1], true
}

// Open reports whether the thread still wants something.
func (t Thread) Open() bool { return t.Status == ThreadOpen || t.Status == "" }

// AgentAnswered reports whether the agent spoke last, and not just to say it's working: the ball is with you.
func (t Thread) AgentAnswered() bool {
	c, ok := t.Last()
	return ok && c.Author == AuthorAgent && !c.Progress
}

// Undelivered are your comments, and those mirrored from GitHub, the agent hasn't been given yet.
func (t Thread) Undelivered() []Comment {
	var out []Comment
	for _, c := range t.Comments {
		if (c.Author == AuthorYou || c.Author == AuthorGitHub) && !c.Delivered && !c.Btw {
			out = append(out, c)
		}
	}
	return out
}

// WaitsOnAgent reports whether the thread is the agent's to move.
func (t Thread) WaitsOnAgent() bool {
	if !t.Open() || t.Btw {
		return false
	}
	c, ok := t.Last()
	if !ok {
		return false
	}
	if c.Author == AuthorAgent {
		return c.Progress
	}
	return c.Author == AuthorYou || c.Author == AuthorGitHub
}

// Drafts are the agent's replies waiting for the user to forward them.
func (t Thread) Drafts() []Comment {
	var out []Comment
	for _, c := range t.Comments {
		if c.Draft {
			out = append(out, c)
		}
	}
	return out
}

// Heading is what a list shows for the thread: its title, else the first line of its first comment.
func (t Thread) Heading() string {
	if t.Title != "" {
		return t.Title
	}
	if len(t.Comments) == 0 {
		return string(t.ID)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(t.Comments[0].Body), "\n")
	return line
}
