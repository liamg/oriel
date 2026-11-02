package core

import "time"

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
	AuthorYou   Author = "you"
	AuthorAgent Author = "agent"
	AuthorHost  Author = "github" // a reviewer's, mirrored from the PR's code host
)

// Anchor is the place a thread is about. A thread with no anchor is about the whole change.
type Anchor struct {
	Project string   `json:"project,omitempty"`
	File    string   `json:"file"` // relative to the project
	Line    int      `json:"line"` // 1-based
	EndLine int      `json:"endLine,omitempty"`
	Col     int      `json:"col,omitempty"`    // 1-based, in characters: where on Line, if it's a point or a span within lines
	EndCol  int      `json:"endCol,omitempty"` // the span's last character, on EndLine (Line without one)
	Side    string   `json:"side,omitempty"`   // "old" for removed code in a diff
	Text    string   `json:"text,omitempty"`   // the anchored lines when it was made, so it can follow edits
	Before  []string `json:"before,omitempty"`
	After   []string `json:"after,omitempty"`
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
	Evidence  []Evidence `json:"evidence,omitempty"`
	Unbacked  string     `json:"unbacked,omitempty"` // a claim with no evidence: "says tests pass, but ran none"
	// Draft is an agent's reply not sent where the thread came from: the user forwards it (a GitHub thread's).
	Draft bool `json:"draft,omitempty"`
	// GitHubID is the comment's on GitHub, HostID on another code host: one mirrored from there, or a draft once
	// forwarded.
	GitHubID int64     `json:"githubId,omitempty"`
	HostID   string    `json:"hostId,omitempty"`
	Sent     time.Time `json:"sent,omitzero"`      // when the user forwarded it
	Redacted bool      `json:"redacted,omitempty"` // its author isn't trusted, so the agent isn't given its text
	Edited   bool      `json:"edited,omitempty"`   // changed where it came from since it was first mirrored
}

// Origins of threads that came from elsewhere: GitHub, or another code host (HostThread.Host names it).
const (
	OriginGitHub   = "github"
	OriginCodeHost = "codehost"
)

// HostThread is where a mirrored thread lives on its code host: a review comment's thread on a PR's line, or the PR's
// conversation (Root 0 and Key "").
type HostThread struct {
	Host     string `json:"host,omitempty"` // the code host's name; "" for GitHub
	Key      string `json:"key,omitempty"`  // the thread's id on a host other than GitHub
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
	ID     ThreadID     `json:"id"`
	Anchor *Anchor      `json:"anchor,omitempty"`
	Title  string       `json:"title,omitempty"`
	Status ThreadStatus `json:"status"`
	Urgent bool         `json:"urgent,omitempty"`
	Btw    bool         `json:"btw,omitempty"`    // a side question thread, answered by a fork
	Busy   bool         `json:"busy,omitempty"`   // the agent has it and hasn't answered
	// Active says the agent is on it now, not only handed it with others: its working reply, a task of it started,
	// or the only one it was handed. Several can be, as subagents work.
	Active bool `json:"active,omitempty"`
	Origin string       `json:"origin,omitempty"` // where it came from: "" for Oriel, OriginGitHub
	Hosted *HostThread  `json:"github,omitempty"` // a review thread mirrored from the PR's code host
	// Outgoing is the user's comment for a pull request's author, in the review they'll submit: not the agent's to
	// answer.
	Outgoing bool      `json:"outgoing,omitempty"`
	Comments []Comment `json:"comments"`
	Touched  []Touched `json:"touched,omitempty"`
	Created  time.Time `json:"created,omitzero"`
	Updated  time.Time `json:"updated,omitzero"`
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
	if t.Outgoing {
		return nil
	}
	var out []Comment
	for _, c := range t.Comments {
		if (c.Author == AuthorYou || c.Author == AuthorHost) && !c.Delivered && !c.Btw && !c.Redacted {
			out = append(out, c)
		}
	}
	return out
}

// WaitsOnAgent reports whether the thread is the agent's to move.
func (t Thread) WaitsOnAgent() bool {
	if !t.Open() || t.Btw || t.Outgoing {
		return false
	}
	// An untrusted author's comment is never the agent's to answer.
	i := len(t.Comments) - 1
	for i >= 0 && t.Comments[i].Redacted {
		i--
	}
	if i < 0 {
		return false
	}
	c := t.Comments[i]
	if c.Author == AuthorAgent {
		return c.Progress
	}
	return c.Author == AuthorYou || c.Author == AuthorHost
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

// Heading is what a list shows for the thread: its title, else the first line of plain text in its first comment.
func (t Thread) Heading() string {
	if t.Title != "" {
		return t.Title
	}
	if len(t.Comments) > 0 {
		if line := Summary(t.Comments[0].Body); line != "" {
			return line
		}
	}
	return string(t.ID)
}
