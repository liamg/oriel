package core

import (
	"slices"
	"time"
)

// WorkerID names a worker: one per machine. The app's own machine runs LocalWorker.
type WorkerID string

const LocalWorker WorkerID = "local"

const (
	KindNewRepo Kind = "newrepo"
	KindDirect  Kind = "direct"
	KindScratch Kind = "scratch"
)

// Project is one repository taking part in a session: a worktree of a checkout, or the directory itself in a
// direct session.
type Project struct {
	Name     string `json:"name"`
	Repo     string `json:"repo,omitempty"` // the checkout it's a worktree of; "" when Dir is the checkout
	Dir      string `json:"dir"`            // absolute, on the session's worker
	Branch   string `json:"branch,omitempty"`
	Base     string `json:"base,omitempty"` // the branch it was cut from
	Primary  bool   `json:"primary,omitempty"`
	Detached bool   `json:"detached,omitempty"` // pinned at Commit: tours and reviews
	Commit   string `json:"commit,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

// PastPR is a pull request a session has finished with; Start next PR moves on from it.
type PastPR struct {
	Project string    `json:"project"`
	Branch  string    `json:"branch"`
	Number  int       `json:"number,omitempty"`
	URL     string    `json:"url,omitempty"`
	Title   string    `json:"title,omitempty"`
	State   string    `json:"state,omitempty"`
	Closed  time.Time `json:"closed,omitzero"`
}

// AgentState is a session's agent: who it is, and what it's doing.
type AgentState struct {
	Provider    string    `json:"provider,omitempty"` // "claude", "codex"; "" for none
	Model       string    `json:"model,omitempty"`
	Effort      string    `json:"effort,omitempty"`
	Session     string    `json:"session,omitempty"` // the provider's own id, to resume by
	Running     bool      `json:"running,omitempty"` // its process is up
	Working     bool      `json:"working,omitempty"` // a turn is running
	TurnStarted time.Time `json:"turnStarted,omitzero"`
	Error       string    `json:"error,omitempty"`  // the last failure, until a turn succeeds
	Policy      string    `json:"policy,omitempty"` // the agent's own gate: ask, light or bypass
	Sandboxed   bool      `json:"sandboxed,omitempty"`
	Network     string    `json:"network,omitempty"` // in a sandbox: ask or open
	Pack        string    `json:"pack,omitempty"`    // the prompt pack it runs with, when not its kind's: work, after a fresh Start work
	Queue       []string  `json:"queue,omitempty"`   // messages waiting for the turn to end
	Stats       Stats     `json:"stats,omitzero"`
	// StoppedBy is who stopped it, while it's stopped: StoppedByYou, StoppedByAgent or StoppedByWorker.
	StoppedBy string    `json:"stoppedBy,omitempty"`
	StoppedAt time.Time `json:"stoppedAt,omitzero"`
	// Accounts are the provider's accounts it ran on, oldest first: recorded when it starts and on a login change.
	Accounts []AccountAt `json:"accounts,omitempty"`
}

// AccountAt is a provider's account (ProviderUsage.Account, and AccountName) a session ran on from At.
type AccountAt struct {
	At      time.Time `json:"at"`
	Account string    `json:"account"`
	Name    string    `json:"name,omitempty"`
}

// RanOn adds the account a session runs on from at, unless it's the one it was on already. An unknown account adds
// nothing.
func (a *AgentState) RanOn(at time.Time, account, name string) {
	if account == "" {
		return
	}
	if n := len(a.Accounts); n > 0 && a.Accounts[n-1].Account == account {
		return
	}
	a.Accounts = append(slices.Clone(a.Accounts), AccountAt{At: at, Account: account, Name: name})
}

// Stopped is the agent stopped at t by by; one that wasn't running keeps who stopped it.
func (a *AgentState) Stopped(by string, t time.Time) {
	if a.Running && by != "" {
		a.StoppedBy, a.StoppedAt = by, t.UTC()
	}
	a.Running, a.Working = false, false
}

// Who stopped an agent.
const (
	StoppedByYou    = "you"
	StoppedByAgent  = "agent"  // its process ended by itself
	StoppedByWorker = "worker" // the worker shut down
	// StoppedByHandOver is a worker that handed over to one from another build, which resumes the agent.
	StoppedByHandOver = "handover"
)

// Stats are what a session's agent has used so far.
type Stats struct {
	Context    int     `json:"context,omitempty"` // tokens in the window now
	ContextMax int     `json:"contextMax,omitempty"`
	Input      int64   `json:"input,omitempty"`
	Output     int64   `json:"output,omitempty"`
	CacheRead  int64   `json:"cacheRead,omitempty"`
	CacheWrite int64   `json:"cacheWrite,omitempty"`
	Turns      int     `json:"turns,omitempty"`
	ToolCalls  int     `json:"toolCalls,omitempty"`
	Cost       float64 `json:"cost,omitempty"`    // estimated, in dollars
	Running    int64   `json:"running,omitempty"` // seconds of turns
}

// CacheHit is the share of input read from the cache, 0–1.
func (t Stats) CacheHit() float64 {
	in := t.Input + t.CacheRead + t.CacheWrite
	if in == 0 {
		return 0
	}
	return float64(t.CacheRead) / float64(in)
}

// TaskStatus is where a task is on the rail.
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskCompleted  TaskStatus = "completed"
)

// Task is one row of the tasks rail. Thread is the request that asked for it; resolving that completes it.
type Task struct {
	ID      string     `json:"id"`
	Subject string     `json:"subject"`
	Status  TaskStatus `json:"status"`
	Thread  ThreadID   `json:"thread,omitempty"`
	Created time.Time  `json:"created,omitzero"`
}

// Subagent is work the agent handed to a helper of its own.
type Subagent struct {
	ID          string     `json:"id"`
	Description string     `json:"description,omitempty"`
	Type        string     `json:"type,omitempty"`
	Prompt      string     `json:"prompt,omitempty"`
	Running     bool       `json:"running,omitempty"`
	Started     time.Time  `json:"started,omitzero"`
	Ended       time.Time  `json:"ended,omitzero"`
	Tools       int        `json:"tools,omitempty"`
	Result      string     `json:"result,omitempty"`
	Threads     []ThreadID `json:"threads,omitempty"` // the requests it works on
}

// AskKind is what an ask waits on the user for.
type AskKind string

const (
	AskPermission AskKind = "permission" // layer 1: the agent's own gate
	AskQuestion   AskKind = "question"   // the agent asked something
	AskNetwork    AskKind = "network"    // layer 3: the egress proxy holds a connection
	AskGateway    AskKind = "gateway"    // layer 3: the gateway holds a call
	AskLogin      AskKind = "login"      // the agent's login on its worker is gone
)

// Login is a provider's sign-in on a worker that has stopped working, and how to sign in again.
type Login struct {
	Provider string   `json:"provider"`
	Account  string   `json:"account,omitempty"` // ProviderUsage.Account; "" when not known
	Worker   WorkerID `json:"worker,omitempty"`
	Detail   string   `json:"detail,omitempty"`   // what the provider said: "OAuth token revoked · Please run /login"
	Terminal string   `json:"terminal,omitempty"` // typed into the agent's own terminal, signs in: "/login"
	Shell    string   `json:"shell,omitempty"`    // run in a shell on the worker, signs in: "claude, then /login"
}

// Ask is something waiting on the user: a permission, a question, a held connection or gateway call.
type Ask struct {
	ID        string     `json:"id"`
	Kind      AskKind    `json:"kind"`
	Tool      string     `json:"tool,omitempty"`
	Summary   string     `json:"summary"`          // "go test ./internal/api/... -run TestUsage"
	Input     string     `json:"input,omitempty"`  // the tool's input, JSON
	Rule      string     `json:"rule,omitempty"`   // the rule "Always" would create: Bash(go test:*)
	Host      string     `json:"host,omitempty"`   // network: host:port
	Detail    string     `json:"detail,omitempty"` // where it came from: "go mod download · first request"
	Questions []Question `json:"questions,omitempty"`
	Login     *Login     `json:"login,omitempty"` // a login ask's
	Created   time.Time  `json:"created,omitzero"`
}

// Question is one of the questions an agent asks, with the options it offers.
type Question struct {
	Header   string   `json:"header,omitempty"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Multi    bool     `json:"multi,omitempty"`
}

// TestRun is a test run recorded as evidence: an agent's "tests pass" links here by ID.
type TestRun struct {
	ID      string    `json:"id"`
	Command string    `json:"command"`
	Dir     string    `json:"dir,omitempty"`
	By      string    `json:"by,omitempty"` // "agent" or "you"
	Passed  int       `json:"passed"`
	Failed  int       `json:"failed"`
	Skipped int       `json:"skipped,omitempty"`
	OK      bool      `json:"ok"`
	Failing []string  `json:"failing,omitempty"`
	Output  string    `json:"output,omitempty"` // the tail of what it printed
	Started time.Time `json:"started,omitzero"`
	Ended   time.Time `json:"ended,omitzero"`
	Thread  ThreadID  `json:"thread,omitempty"`
	Item    string    `json:"item,omitempty"` // the transcript item that ran it
}

// sessionView is a read-only Session over a copy of its state.
type sessionView struct{ s SessionState }

func (v sessionView) ID() SessionID { return v.s.ID }

func (v sessionView) Name() string {
	if v.s.Name != "" {
		return v.s.Name
	}
	return string(v.s.ID)
}

func (v sessionView) Kind() Kind             { return v.s.Kind }
func (v sessionView) Status() Status         { return v.s.Status }
func (v sessionView) Unseen() int            { return v.s.Unseen }
func (v sessionView) Projects() []Project    { return slices.Clone(v.s.Projects) }
func (v sessionView) Threads() []Thread      { return slices.Clone(v.s.Threads) }
func (v sessionView) Tasks() []Task          { return slices.Clone(v.s.Tasks) }
func (v sessionView) Agent() AgentState      { return v.s.Agent }
func (v sessionView) Snapshot() SessionState { return v.s }

// Primary is the session's first project, or its primary one: where a relative path means.
func (s SessionState) Primary() (Project, bool) {
	for _, p := range s.Projects {
		if p.Primary {
			return p, true
		}
	}
	if len(s.Projects) > 0 {
		return s.Projects[0], true
	}
	return Project{}, false
}

// Thread finds a thread by id.
func (s SessionState) Thread(id ThreadID) (Thread, bool) {
	if i := slices.IndexFunc(s.Threads, func(t Thread) bool { return t.ID == id }); i >= 0 {
		return s.Threads[i], true
	}
	return Thread{}, false
}

// Run finds a test run by id.
func (s SessionState) Run(id string) (TestRun, bool) {
	if i := slices.IndexFunc(s.Runs, func(r TestRun) bool { return r.ID == id }); i >= 0 {
		return s.Runs[i], true
	}
	return TestRun{}, false
}
