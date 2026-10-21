package core

import (
	"slices"
	"time"
)

// State is the materialised result of every event applied so far. It encodes to the same JSON
// whenever the same events have been applied.
//
// Apply never changes a slice nested in it in place: it builds a new one, so a copy of the state can share them.
type State struct {
	Seq           uint64          `json:"seq"` // events applied
	Sessions      []SessionState  `json:"sessions"`
	Focus         SessionID       `json:"focus,omitempty"` // the session in front
	Tabs          []SessionID     `json:"tabs,omitempty"`  // the sessions open in the window, in the order they opened
	Notifications []Notification  `json:"notifications"`   // oldest first
	Machine       MachineState    `json:"machine,omitzero"`
	Usage         []ProviderUsage `json:"usage,omitempty"` // each provider's plan windows
	Repos         []Repo          `json:"repos,omitempty"` // the registered checkouts
}

type SessionState struct {
	ID         SessionID      `json:"id"`
	Name       string         `json:"name,omitempty"`
	Kind       Kind           `json:"kind"`
	Status     Status         `json:"status"`
	Unseen     int            `json:"unseen,omitempty"` // errors since the session last had focus
	Dir        string         `json:"dir,omitempty"`    // the workspace; the directory itself when direct
	Branch     string         `json:"branch,omitempty"`
	Projects   []Project      `json:"projects,omitempty"`
	Prompt     string         `json:"prompt,omitempty"`
	Ticket     string         `json:"ticket,omitempty"`
	PR         string         `json:"pr,omitempty"`
	PRRef      string         `json:"prRef,omitempty"`  // where a review fetches its PR from, when not GitHub's pull/N/head
	Source     string         `json:"source,omitempty"` // "crit" for a crit workspace, which the session host drops
	Job        string         `json:"job,omitempty"`    // the saved job this session is a run of
	Created    time.Time      `json:"created,omitzero"`
	Archived   bool           `json:"archived,omitempty"`
	Unplanned  bool           `json:"unplanned,omitempty"` // work started with no plan or ticket
	StartWork  bool           `json:"startWork,omitempty"` // its worktrees come at Start work: a plan
	Started    bool           `json:"started,omitempty"`   // a plan session whose work has started
	Past       []PastPR       `json:"past,omitempty"`
	Agent      AgentState     `json:"agent,omitzero"`
	Threads    []Thread       `json:"threads,omitempty"`
	Tasks      []Task         `json:"tasks,omitempty"`
	Subagents  []Subagent     `json:"subagents,omitempty"`
	Asks       []Ask          `json:"asks,omitempty"`
	Transcript []Item         `json:"transcript,omitempty"`
	Runs       []TestRun      `json:"runs,omitempty"`
	Rules      []string       `json:"rules,omitempty"`      // "Always in this workspace": Bash(go test:*), net(*.golang.org:443)
	Reviews    []Review       `json:"reviews,omitempty"`    // what's been marked reviewed in the diff, per file
	CommitGate string         `json:"commitGate,omitempty"` // its kind's commit gate: hard, soft or off; "" for DefaultGate
	Overrides  []GateOverride `json:"overrides,omitempty"`  // commits made past the gate
	AIReviews  []AIReview     `json:"aiReviews,omitempty"`
	Findings   []Finding      `json:"findings,omitempty"` // the AI reviews' findings
	Review     *ReviewDraft   `json:"review,omitempty"`   // a review session's review, as the user is writing it
	// Verifications are the verify quizzes taken before raising its PRs, the last one standing.
	Verifications []Verification `json:"verifications,omitempty"`
	ProjectUI     ProjectUI      `json:"projectUI,omitzero"`
}

// MachineState is this machine as the session host reads it: what it runs, and its load.
type MachineState struct {
	Host      string    `json:"host,omitempty"`
	OS        string    `json:"os,omitempty"`
	Arch      string    `json:"arch,omitempty"`
	Version   string    `json:"version,omitempty"`
	Providers []string  `json:"providers,omitempty"` // the agents it can run
	Sandboxes []string  `json:"sandboxes,omitempty"` // the sandbox backends it can run
	Cores     int       `json:"cores,omitempty"`
	CPU       float64   `json:"cpu,omitempty"` // 0–1, the whole machine
	Mem       uint64    `json:"mem,omitempty"` // bytes in use
	MemTotal  uint64    `json:"memTotal,omitempty"`
	Sampled   time.Time `json:"sampled,omitzero"`
	SignedOut []Login   `json:"signedOut,omitempty"` // providers' logins here that have stopped working
}

// UsageWindow is one of a plan's limits: the 5-hour window, the weekly one.
type UsageWindow struct {
	Name   string        `json:"name"`
	Used   float64       `json:"used"` // 0–1
	Resets time.Time     `json:"resets,omitzero"`
	Length time.Duration `json:"length,omitempty"` // how long the window is: 5h, a week
}

// Opened is when the window began: Length before it resets; zero when either is unknown.
func (w UsageWindow) Opened() time.Time {
	if w.Resets.IsZero() || w.Length == 0 {
		return time.Time{}
	}
	return w.Resets.Add(-w.Length)
}

// ProviderUsage is how much of an account's plan with a provider is used. Plans belong to accounts: two accounts'
// windows are separate limits, never added up.
type ProviderUsage struct {
	Provider string `json:"provider"`
	// Account identifies the login (Claude's account and organisation, Codex's email); AccountName is how it reads
	// with no label of the user's: an email or an organisation.
	Account     string        `json:"account,omitempty"`
	AccountName string        `json:"accountName,omitempty"`
	Plan        string        `json:"plan,omitempty"` // "Claude Max", "ChatGPT Pro"
	Tier        string        `json:"tier,omitempty"` // the plan's tier, as its chip reads: "Max 20×", "Team", "Pro"; "" when not known
	Windows     []UsageWindow `json:"windows,omitempty"`
	Credits     string        `json:"credits,omitempty"`
	Sampled     time.Time     `json:"sampled,omitzero"`
	Source      string        `json:"source,omitempty"` // where the windows are read from: "Codex's account/rateLimits/read"
}

// Same says whether u and o are the same account's plan.
func (u ProviderUsage) Same(o ProviderUsage) bool {
	return u.Provider == o.Provider && u.Account == o.Account
}

// SignedOut are the logins to u's account that have stopped working. A login gone before its account was known
// counts for each of its provider's.
func (s State) SignedOut(u ProviderUsage) []Login {
	var out []Login
	for _, l := range s.Machine.SignedOut {
		if l.Provider == u.Provider && (l.Account == u.Account || l.Account == "") {
			out = append(out, l)
		}
	}
	return out
}

func (s *State) session(id SessionID) *SessionState {
	if i := slices.IndexFunc(s.Sessions, func(se SessionState) bool { return se.ID == id }); i >= 0 {
		return &s.Sessions[i]
	}
	return nil
}

// Clone returns a copy that shares nothing mutable with s.
func (s State) Clone() State {
	s.Sessions = slices.Clone(s.Sessions)
	if s.Sessions == nil {
		s.Sessions = []SessionState{}
	}
	s.Notifications = slices.Clone(s.Notifications)
	if s.Notifications == nil {
		s.Notifications = []Notification{}
	}
	s.Machine.SignedOut = slices.Clone(s.Machine.SignedOut)
	s.Usage = slices.Clone(s.Usage)
	s.Repos = slices.Clone(s.Repos)
	return s
}

// Session returns the session with id as a read-only Session.
func (s State) Session(id SessionID) (Session, bool) {
	if se := s.session(id); se != nil {
		return sessionView{*se}, true
	}
	return nil, false
}

// SessionList returns every session, in the order they opened.
func (s State) SessionList() []Session {
	out := make([]Session, len(s.Sessions))
	for i, se := range s.Sessions {
		out[i] = sessionView{se}
	}
	return out
}
