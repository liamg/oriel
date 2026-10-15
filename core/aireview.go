package core

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

func init() {
	RegisterEvent[AIReviewChanged]("AIReviewChanged")
	RegisterEvent[FindingsRecorded]("FindingsRecorded")
}

// OriginAIReview is Thread.Origin for a thread opened from an AI review's finding.
const OriginAIReview = "ai-review"

// Severities are a review finding's ratings, worst first: the threads' scale (Comment.Severity).
var Severities = []string{"critical", "high", "medium", "low", "nit"}

// SeverityRank is a severity's place in Severities, len(Severities) for one it doesn't know.
func SeverityRank(s string) int {
	if i := slices.Index(Severities, s); i >= 0 {
		return i
	}
	return len(Severities)
}

// AIReview is one run of the cross-model AI review over a session's change (agents.md, Guardrails).
type AIReview struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"` // the reviewer's
	Model    string            `json:"model,omitempty"`
	Author   string            `json:"author,omitempty"` // the session agent's provider and model it reviewed: "claude · opus"
	Cross    bool              `json:"cross,omitempty"`  // a different model from the author's
	Projects []ReviewedProject `json:"projects,omitempty"`
	Started  time.Time         `json:"started,omitzero"`
	Ended    time.Time         `json:"ended,omitzero"`
	Running  bool              `json:"running,omitempty"`
	Error    string            `json:"error,omitempty"`
	Verdict  string            `json:"verdict,omitempty"` // the reviewer's last words
}

// ReviewedProject is the change of one project a review read: from Since to the work tree as it was, at Head.
type ReviewedProject struct {
	Project string `json:"project"`
	Since   string `json:"since"` // abbreviated commits
	Head    string `json:"head,omitempty"`
	From    string `json:"from,omitempty"` // what the change was read from: Since, or the last review's Tree
	Tree    string `json:"tree,omitempty"` // the work tree it read, written as a git tree (gitx.Repo.Snapshot)
}

// Since is the commit the review's change starts from: its first project's.
func (r AIReview) Since() string {
	if len(r.Projects) == 0 {
		return ""
	}
	return r.Projects[0].Since
}

// Finding is one thing an AI review found.
type Finding struct {
	ID       string   `json:"id"`
	Review   string   `json:"review"`             // the AIReview it came from
	Severity string   `json:"severity"`           // one of Severities
	Category string   `json:"category,omitempty"` // correctness, tests, api, style…
	Title    string   `json:"title"`
	Project  string   `json:"project,omitempty"`
	File     string   `json:"file,omitempty"` // relative to the project
	Line     int      `json:"line,omitempty"`
	Body     string   `json:"body,omitempty"` // markdown
	Repo     string   `json:"repo,omitempty"` // a repository it's about that the session hasn't got
	Thread   ThreadID `json:"thread,omitempty"`
	// Dismissed is a finding the user set aside.
	Dismissed bool `json:"dismissed,omitempty"`
}

// Where is the finding's place as a card shows it: "billing/invoice.go:118".
func (f Finding) Where() string {
	switch {
	case f.File == "":
		return ""
	case f.Line > 0:
		return fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	return f.File
}

// LastAIReview is the session's latest AI review.
func (s SessionState) LastAIReview() (AIReview, bool) {
	if len(s.AIReviews) == 0 {
		return AIReview{}, false
	}
	return s.AIReviews[len(s.AIReviews)-1], true
}

// SortedFindings are the session's findings, worst first, then by place.
func (s SessionState) SortedFindings() []Finding {
	out := slices.Clone(s.Findings)
	slices.SortStableFunc(out, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(SeverityRank(a.Severity), SeverityRank(b.Severity)), strings.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line))
	})
	return out
}

// Finding finds a finding by ID.
func (s SessionState) Finding(id string) (Finding, bool) {
	if i := slices.IndexFunc(s.Findings, func(f Finding) bool { return f.ID == id }); i >= 0 {
		return s.Findings[i], true
	}
	return Finding{}, false
}

// FindingOpen reports whether a finding still stands: not dismissed, and not turned into a thread that's been resolved or
// dismissed since.
func (s SessionState) FindingOpen(f Finding) bool {
	if f.Dismissed {
		return false
	}
	if f.Thread == "" {
		return true
	}
	t, ok := s.Thread(f.Thread)
	return !ok || t.Open() || t.Status == ThreadDeferred
}

// AIReviewGate is the "AI review" row of the checks before a change ships: ok once a review has run and every
// critical and high finding is dealt with, and what to say about it.
func (s SessionState) AIReviewGate() (ok bool, label string) {
	r, has := s.LastAIReview()
	switch {
	case !has:
		return false, "not run"
	case r.Running:
		return false, "running"
	case r.Error != "" && len(s.Findings) == 0:
		return false, "failed: " + r.Error
	}
	blocking, rest := 0, 0
	for _, f := range s.Findings {
		if !s.FindingOpen(f) {
			continue
		}
		if SeverityRank(f.Severity) <= SeverityRank("high") {
			blocking++
		} else {
			rest++
		}
	}
	if blocking > 0 {
		return false, fmt.Sprintf("%d high or critical open", blocking)
	}
	if rest > 0 {
		return true, fmt.Sprintf("clear · %d minor open", rest)
	}
	return true, "clear"
}

// AIReviewChanged is an AI review starting, finishing or failing: the review whole, replacing one of its ID.
type AIReviewChanged struct {
	Session SessionID `json:"session"`
	Review  AIReview  `json:"review"`
}

func (e AIReviewChanged) Scope() SessionID { return e.Session }

func (e AIReviewChanged) Apply(s *State) error {
	if e.Review.ID == "" {
		return errors.New("an AI review needs an ID")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	out := slices.Clone(se.AIReviews)
	if i := slices.IndexFunc(out, func(r AIReview) bool { return r.ID == e.Review.ID }); i >= 0 {
		out[i] = e.Review
	} else {
		out = append(out, e.Review)
	}
	se.AIReviews = out
	return nil
}

// FindingsRecorded adds findings, or replaces those of the same ID: a review's new ones, one dismissed or turned
// into a thread.
type FindingsRecorded struct {
	Session  SessionID `json:"session"`
	Findings []Finding `json:"findings"`
}

func (e FindingsRecorded) Scope() SessionID { return e.Session }

func (e FindingsRecorded) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	out := slices.Clone(se.Findings)
	for _, f := range e.Findings {
		if f.ID == "" || f.Title == "" {
			return errors.New("a finding needs an ID and a title")
		}
		if i := slices.IndexFunc(out, func(o Finding) bool { return o.ID == f.ID }); i >= 0 {
			out[i] = f
		} else {
			out = append(out, f)
		}
	}
	se.Findings = out
	return nil
}
