package core

import (
	"maps"
	"slices"
	"time"
)

func init() {
	RegisterEvent[ReviewDrafted]("ReviewDrafted")
}

// What the user has made of a comment a review found, for the review they'll submit.
const (
	PickKeep = "keep" // it goes in the review
	PickDrop = "drop" // it doesn't
)

// ReviewDraft is the review the user is writing of a review session's pull request: what they've made of each comment
// the review found, the summary, and the reviews they've sent.
type ReviewDraft struct {
	Picks   map[string]ReviewPick `json:"picks,omitempty"` // by the comment's thread or finding id
	Summary string                `json:"summary,omitempty"`
	// SummaryAsk is the request asking the agent to draft the summary; its answer fills Summary.
	SummaryAsk ThreadID     `json:"summaryAsk,omitempty"`
	Sent       []SentReview `json:"sent,omitempty"`
}

// ReviewPick is what the user has made of one comment.
type ReviewPick struct {
	State    string `json:"state,omitempty"`    // PickKeep, PickDrop; "" for not decided yet
	Body     string `json:"body,omitempty"`     // the words the author gets, when not the comment's own
	Severity string `json:"severity,omitempty"` // the user's rating, over the reviewer's
	// Rework is the thread the agent was asked to reword it in, and ReworkAt how many comments it had then: the
	// agent's next is the new wording.
	Rework   ThreadID `json:"rework,omitempty"`
	ReworkAt int      `json:"reworkAt,omitempty"`
	Sent     string   `json:"sent,omitempty"` // the text posted, once it's gone
}

// SentReview is a review the user submitted.
type SentReview struct {
	Verdict  string    `json:"verdict"`
	Commit   string    `json:"commit,omitempty"`
	Comments int       `json:"comments,omitempty"`
	At       time.Time `json:"at"`
}

// Clone is a copy that shares nothing mutable with d.
func (d ReviewDraft) Clone() ReviewDraft {
	d.Picks = maps.Clone(d.Picks)
	if d.Picks == nil {
		d.Picks = map[string]ReviewPick{}
	}
	d.Sent = slices.Clone(d.Sent)
	return d
}

// ReviewDrafted sets a session's review draft whole.
type ReviewDrafted struct {
	Session SessionID   `json:"session"`
	Draft   ReviewDraft `json:"draft"`
}

func (e ReviewDrafted) Scope() SessionID { return e.Session }

func (e ReviewDrafted) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	d := e.Draft.Clone()
	se.Review = &d
	return nil
}
