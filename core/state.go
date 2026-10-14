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
	Notifications []Notification  `json:"notifications"`   // oldest first
	Workers       []WorkerState   `json:"workers,omitempty"`
	Usage         []ProviderUsage `json:"usage,omitempty"` // each provider's plan windows
}

type SessionState struct {
	ID         SessionID  `json:"id"`
	Name       string     `json:"name,omitempty"`
	Kind       Kind       `json:"kind"`
	Status     Status     `json:"status"`
	Unseen     int        `json:"unseen,omitempty"` // errors since the session last had focus
	Worker     WorkerID   `json:"worker,omitempty"`
	Dir        string     `json:"dir,omitempty"` // the workspace on the worker; the directory itself when direct
	Branch     string     `json:"branch,omitempty"`
	Projects   []Project  `json:"projects,omitempty"`
	Prompt     string     `json:"prompt,omitempty"`
	Ticket     string     `json:"ticket,omitempty"`
	PR         string     `json:"pr,omitempty"`
	Source     string     `json:"source,omitempty"` // "crit" for a crit workspace read in place
	Created    time.Time  `json:"created,omitzero"`
	Archived   bool       `json:"archived,omitempty"`
	Unplanned  bool       `json:"unplanned,omitempty"` // work started with no plan or ticket
	StartWork  bool       `json:"startWork,omitempty"` // its worktrees come at Start work: a plan
	Started    bool       `json:"started,omitempty"`   // a plan session whose work has started
	Past       []PastPR   `json:"past,omitempty"`
	Agent      AgentState `json:"agent,omitzero"`
	Threads    []Thread   `json:"threads,omitempty"`
	Tasks      []Task     `json:"tasks,omitempty"`
	Subagents  []Subagent `json:"subagents,omitempty"`
	Asks       []Ask      `json:"asks,omitempty"`
	Transcript []Item     `json:"transcript,omitempty"`
	Runs       []TestRun  `json:"runs,omitempty"`
	Rules      []string   `json:"rules,omitempty"` // "Always in this workspace": Bash(go test:*), net(*.golang.org:443)
	ProjectUI  ProjectUI  `json:"projectUI,omitzero"`
}

// WorkerState is a worker as the app sees it.
type WorkerState struct {
	ID        WorkerID  `json:"id"`
	Name      string    `json:"name,omitempty"`
	Host      string    `json:"host,omitempty"`
	OS        string    `json:"os,omitempty"`
	Connected bool      `json:"connected"`
	Cores     int       `json:"cores,omitempty"`
	CPU       float64   `json:"cpu,omitempty"` // 0–1, the whole machine
	Mem       uint64    `json:"mem,omitempty"` // bytes in use
	MemTotal  uint64    `json:"memTotal,omitempty"`
	Sampled   time.Time `json:"sampled,omitzero"`
}

// UsageWindow is one of a plan's limits: the 5-hour window, the weekly one.
type UsageWindow struct {
	Name   string    `json:"name"`
	Used   float64   `json:"used"` // 0–1
	Resets time.Time `json:"resets,omitzero"`
}

// ProviderUsage is how much of a provider's plan is used.
type ProviderUsage struct {
	Provider string        `json:"provider"`
	Plan     string        `json:"plan,omitempty"` // "Claude Max", "ChatGPT Pro"
	Windows  []UsageWindow `json:"windows,omitempty"`
	Credits  string        `json:"credits,omitempty"`
	Sampled  time.Time     `json:"sampled,omitzero"`
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
	s.Workers = slices.Clone(s.Workers)
	s.Usage = slices.Clone(s.Usage)
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
