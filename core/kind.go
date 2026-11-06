package core

import (
	"encoding/json"
	"slices"
	"sync"
)

// Worktrees is when and how a kind of session gets its projects' worktrees.
type Worktrees string

const (
	WorktreesNow       Worktrees = "now"        // on a fresh branch from the fetched base, at once
	WorktreesStartWork Worktrees = "start-work" // at Start work; until then the agent reads the checkouts
	WorktreesPR        Worktrees = "pr"         // the pull request's head, detached and read-only
	WorktreesNone      Worktrees = "none"       // the directory itself (direct)
	WorktreesInit      Worktrees = "init"       // a new repo, made in place
)

// From is what a kind of session starts from, beside its prompt: what the launcher asks for.
type From string

const (
	FromPrompt From = "prompt" // a prompt or a ticket
	FromPR     From = "pr"     // a pull request's URL
	FromName   From = "name"   // a new repo's name, template and parent directory
	FromDir    From = "dir"    // a directory, opened as it is
)

// Capability is something a kind of session can do, which features ask about instead of naming kinds.
type Capability string

const (
	AddProjects Capability = "add-projects" // more repos can join it
	Ships       Capability = "ships"        // it raises pull requests of its own
	OwnsChanges Capability = "owns-changes" // what's changed in it is its own, to commit, fix and ship
	NextPR      Capability = "next-pr"      // once its PR is done with, it moves on to a fresh branch
	Delete      Capability = "delete"       // its workspace is Oriel's, to delete
	OwnsBranch  Capability = "owns-branch"  // Oriel made its branches: it names, renames and deletes them
	// CommentsInDiff keeps its agent's line comments on lines of its pull request's diff, as GitHub's review takes them.
	CommentsInDiff Capability = "comments-in-diff"
	RestartsAgent  Capability = "restarts-agent" // its agent is started again when it exits on its own
)

// KindSpec is a kind of session as data: what it starts from and what its agent is told.
type KindSpec struct {
	Kind      Kind      `json:"kind"`
	Title     string    `json:"title"`
	Desc      string    `json:"desc,omitempty"`  // a few words, under the title in the launcher
	About     string    `json:"about,omitempty"` // what starting it does, a sentence
	From      From      `json:"from,omitempty"`  // FromPrompt if ""
	Pack      string    `json:"pack"`            // the prompt pack
	Worktrees Worktrees `json:"worktrees"`
	NeedsRepo bool      `json:"needsRepo,omitempty"`
	// Launcher offers the kind as a mode in the new-session launcher; the rest start from their own pages or
	// session.start.
	Launcher bool `json:"launcher,omitempty"`
	Order    int  `json:"order,omitempty"` // the launcher's order: the built-ins are 10 to 60
	// PromptFile is a file at the workspace root the prompt is written to as the session starts: a job's JOB.md.
	PromptFile string `json:"promptFile,omitempty"`
	// AgentGate is the agent's own gate its sessions start with when theirs doesn't say: ask, light, auto or
	// bypass; ask if "". A job's is auto: nobody is there to answer.
	AgentGate string `json:"agentGate,omitempty"`
	// Opens are commands run once the window is on a new session of the kind, before session.start's own then.
	Opens []KindCall   `json:"opens,omitempty"`
	Caps  []Capability `json:"caps,omitempty"`
}

// Can says whether the kind has capability c.
func (k KindSpec) Can(c Capability) bool { return slices.Contains(k.Caps, c) }

// InPlace says its sessions work in the user's own directory, with no worktree.
func (k KindSpec) InPlace() bool { return k.Worktrees == WorktreesNone }

// KindCall is a command and its args.
type KindCall struct {
	Command string         `json:"command"`
	Args    map[string]any `json:"args,omitempty"`
}

// KindData is what a session of one kind keeps that no other kind has: a review's pull request, a job's name.
type KindData interface{ DataKind() Kind }

// ReviewData is a review session's: the pull request it reads, and the review the user is writing of it.
type ReviewData struct {
	PR    string       `json:"pr,omitempty"`
	PRRef string       `json:"prRef,omitempty"` // where it's fetched from, when not GitHub's pull/N/head
	Draft *ReviewDraft `json:"draft,omitempty"`
}

// JobData is a job's run's: the saved job it's a run of.
type JobData struct {
	Job string `json:"job,omitempty"`
}

// WorkData is a work session's.
type WorkData struct {
	Unplanned bool `json:"unplanned,omitempty"` // started with no plan or ticket
}

// OtherData is the data of a kind core has no type for, kept as it was read.
type OtherData struct {
	Of  Kind
	Raw json.RawMessage
}

func (ReviewData) DataKind() Kind  { return KindReview }
func (JobData) DataKind() Kind     { return KindJob }
func (WorkData) DataKind() Kind    { return KindWork }
func (d OtherData) DataKind() Kind { return d.Of }

func (d OtherData) MarshalJSON() ([]byte, error) {
	if len(d.Raw) == 0 {
		return []byte("null"), nil
	}
	return d.Raw, nil
}

var dataTypes sync.Map // Kind → func(json.RawMessage) (KindData, error)

// RegisterData says kind T.DataKind()'s sessions keep a T, so it's read back as one.
func RegisterData[T KindData]() {
	var zero T
	dataTypes.Store(zero.DataKind(), func(raw json.RawMessage) (KindData, error) {
		var t T
		err := json.Unmarshal(raw, &t)
		return t, err
	})
}

func init() {
	RegisterData[ReviewData]()
	RegisterData[JobData]()
	RegisterData[WorkData]()
}

func decodeData(k Kind, raw json.RawMessage) (KindData, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if fn, ok := dataTypes.Load(k); ok {
		return fn.(func(json.RawMessage) (KindData, error))(raw)
	}
	return OtherData{Of: k, Raw: slices.Clone(raw)}, nil
}

// Data is the session's own data for kind k: nil unless it's a session of k that has some.
func (s SessionState) Data(k Kind) KindData {
	if s.Kind != k || s.data == nil || s.data.DataKind() != k {
		return nil
	}
	return s.data
}

// SetData sets the session's own data; d must be its kind's.
func (s *SessionState) SetData(d KindData) { s.data = d }

// DataOf is the session's data as its kind's type T, and whether it's a session of T's kind. A session of the
// kind with none has T's zero value.
func DataOf[T KindData](s SessionState) (T, bool) {
	var zero T
	if s.Kind != zero.DataKind() {
		return zero, false
	}
	d, _ := s.data.(T)
	return d, true
}

// Data is what a session started from spec keeps for its kind.
func (spec SessionSpec) Data() KindData {
	switch spec.Kind {
	case KindReview:
		return ReviewData{PR: spec.PR, PRRef: spec.PRRef}
	case KindJob:
		return JobData{Job: spec.Job}
	case KindWork:
		if spec.Ticket == "" {
			return WorkData{Unplanned: true}
		}
	}
	return nil
}

type plainSession SessionState

func (s SessionState) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		plainSession
		Data KindData `json:"data,omitempty"`
	}{plainSession(s), s.data})
}

// UnmarshalJSON reads a session, moving what older logs kept flat on it into its kind's data.
func (s *SessionState) UnmarshalJSON(b []byte) error {
	var v struct {
		plainSession
		Data      json.RawMessage `json:"data,omitempty"`
		PR        string          `json:"pr,omitempty"`
		PRRef     string          `json:"prRef,omitempty"`
		Job       string          `json:"job,omitempty"`
		Review    *ReviewDraft    `json:"review,omitempty"`
		Unplanned bool            `json:"unplanned,omitempty"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*s = SessionState(v.plainSession)
	d, err := decodeData(s.Kind, v.Data)
	if err != nil {
		return err
	}
	if d == nil {
		switch {
		case s.Kind == KindReview && (v.PR != "" || v.PRRef != "" || v.Review != nil):
			d = ReviewData{PR: v.PR, PRRef: v.PRRef, Draft: v.Review}
		case s.Kind == KindJob && v.Job != "":
			d = JobData{Job: v.Job}
		case s.Kind == KindWork && v.Unplanned:
			d = WorkData{Unplanned: true}
		}
	}
	s.data = d
	return nil
}
