package core

import (
	"encoding/json"
	"strconv"
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
	AuthorYou   Author = "you"
	AuthorAgent Author = "agent"
	AuthorHost  Author = "codehost" // a reviewer's, mirrored from the PR's code host
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

// Reaction is how many people reacted to a comment with an emoji, Mine when the user is one of them. Who are some
// or all of them, by login, where the host names them.
type Reaction struct {
	Emoji string   `json:"emoji"`
	Count int      `json:"count"`
	Mine  bool     `json:"mine,omitempty"`
	Who   []string `json:"who,omitempty"`
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
	Asked     bool       `json:"asked,omitempty"`     // a question the agent asked with its ask tool, or your answer to it
	Delivered bool       `json:"delivered,omitempty"` // a comment of yours the agent has been given
	Images    []string   `json:"images,omitempty"`
	Model     string     `json:"model,omitempty"` // the agent's model and effort when it wrote this
	Severity  string     `json:"severity,omitempty"`
	Category  string     `json:"category,omitempty"` // a rated finding's kind: one of Categories, or another word
	Evidence  []Evidence `json:"evidence,omitempty"`
	Unbacked  string     `json:"unbacked,omitempty"` // a claim with no evidence: "says tests pass, but ran none"
	// Draft is an agent's reply not sent where the thread came from: the user forwards it (a code host's thread's).
	Draft bool `json:"draft,omitempty"`
	// HostID is the comment's id on its code host: one mirrored from there, or a draft once forwarded.
	HostID    string     `json:"hostId,omitempty"`
	Sent      time.Time  `json:"sent,omitzero"`       // when the user forwarded it
	Redacted  bool       `json:"redacted,omitempty"`  // its author isn't trusted, so the agent isn't given its text
	Edited    bool       `json:"edited,omitempty"`    // changed where it came from since it was first mirrored
	Mine      bool       `json:"mine,omitempty"`      // mirrored, and written there by the user: not the agent's to answer
	Reactions []Reaction `json:"reactions,omitempty"` // mirrored: the emoji people reacted with where it came from
	// Proposes is an agent's reply offering a change it hasn't made: while it's the thread's last word, the thread
	// offers Do it.
	Proposes bool `json:"proposes,omitempty"`
}

// OriginCodeHost is a thread mirrored from a code host's pull request (HostThread.Host names it).
const OriginCodeHost = "codehost"

// HostThread is where a mirrored thread lives on its code host: a review comment's thread on a PR's line, or the PR's
// conversation (Key "").
type HostThread struct {
	Host     string `json:"host,omitempty"` // the code host's name: "github", "gitlab"
	Key      string `json:"key,omitempty"`  // the thread's id there
	Project  string `json:"project"`
	Repo     string `json:"repo"` // owner/name
	PR       int    `json:"pr"`
	Reviewer string `json:"reviewer,omitempty"` // who started it
	State    string `json:"state,omitempty"`    // the reviewer's verdict: registry's changes, approved, commented
	URL      string `json:"url,omitempty"`
	Resolved bool   `json:"resolved,omitempty"` // resolved there, as last mirrored
	Outdated bool   `json:"outdated,omitempty"` // its line is gone from the PR's head
}

// UnmarshalJSON reads where a mirrored thread lives, taking one older logs kept as GitHub's alone (no host, its
// first comment's id as root) as GitHub's under its key, and a verdict they kept in GitHub's words in a code host's.
func (g *HostThread) UnmarshalJSON(b []byte) error {
	type plain HostThread
	var v struct {
		plain
		Root int64 `json:"root"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*g = HostThread(v.plain)
	if s, ok := githubVerdicts[g.State]; ok {
		g.State = s
	}
	if g.Host == "" {
		g.Host = "github"
		if v.Root != 0 {
			g.Key = strconv.FormatInt(v.Root, 10)
		}
	}
	return nil
}

// githubVerdicts are GitHub's review states, as older logs kept them, in a code host's words.
var githubVerdicts = map[string]string{"APPROVED": "approved", "CHANGES_REQUESTED": "changes", "COMMENTED": "commented"}

// UnmarshalJSON reads a comment, taking the GitHub id older logs kept apart (githubId) as its HostID, and the author
// they named github as the code host's.
func (c *Comment) UnmarshalJSON(b []byte) error {
	type plain Comment
	var v struct {
		plain
		GitHubID int64 `json:"githubId"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*c = Comment(v.plain)
	if c.HostID == "" && v.GitHubID != 0 {
		c.HostID = strconv.FormatInt(v.GitHubID, 10)
	}
	if c.Author == "github" {
		c.Author = AuthorHost
	}
	return nil
}

// UnmarshalJSON reads a thread, taking one older logs said came from GitHub (origin "github", where it lives under
// "github") as a code host's.
func (t *Thread) UnmarshalJSON(b []byte) error {
	type plain Thread
	var v struct {
		*plain
		GitHub *HostThread `json:"github"`
	}
	v.plain = (*plain)(t)
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	if t.Hosted == nil {
		t.Hosted = v.GitHub
	}
	if t.Origin == "github" {
		t.Origin = OriginCodeHost
	}
	return nil
}

// From is where the thread came from: its code host's name for a mirrored one, else its Origin.
func (t Thread) From() string {
	if t.Hosted != nil && t.Hosted.Host != "" {
		return t.Hosted.Host
	}
	return t.Origin
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
	Btw    bool         `json:"btw,omitempty"`  // a side question thread, answered by a fork
	Busy   bool         `json:"busy,omitempty"` // the agent has it and hasn't answered
	// Active says the agent is on it now, not only handed it with others: its working reply, a task of it started,
	// or the only one it was handed. Several can be, as subagents work.
	Active bool        `json:"active,omitempty"`
	Origin string      `json:"origin,omitempty"` // where it came from: "" for Oriel, OriginCodeHost
	Hosted *HostThread `json:"hosted,omitempty"` // a review thread mirrored from the PR's code host
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
	return c.Author == AuthorYou || c.Author == AuthorHost && !c.Mine
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
