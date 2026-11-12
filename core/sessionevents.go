package core

import (
	"fmt"
	"reflect"
	"slices"
)

func init() {
	RegisterEvent[SessionSynced]("SessionSynced")
	RegisterEvent[SessionRenamed]("SessionRenamed")
	RegisterEvent[SessionArchived]("SessionArchived")
	RegisterEvent[ProjectsChanged]("ProjectsChanged")
	RegisterEvent[AgentChanged]("AgentChanged")
	RegisterEvent[ThreadUpdated]("ThreadUpdated")
	RegisterEvent[ThreadBusy]("ThreadBusy")
	RegisterEvent[ThreadsRemoved]("ThreadsRemoved")
	RegisterEvent[TaskChanged]("TaskChanged")
	RegisterEvent[SubagentChanged]("SubagentChanged")
	RegisterEvent[AskOpened]("AskOpened")
	RegisterEvent[AskClosed]("AskClosed")
	RegisterEvent[TranscriptAppended]("TranscriptAppended")
	RegisterEvent[TestRunRecorded]("TestRunRecorded")
	RegisterEvent[UsageSampled]("UsageSampled")
	RegisterEvent[UsageRead]("UsageRead")
	RegisterEvent[MachineChanged]("MachineChanged")
	RegisterEvent[RuleAdded]("RuleAdded")
	RegisterEvent[DraftSaved]("DraftSaved")
}

// upsert is xs with x in place of the element same matches, or with x added: a new slice, never xs changed.
func upsert[T any](xs []T, x T, same func(T) bool) []T {
	out := slices.Clone(xs)
	if i := slices.IndexFunc(out, same); i >= 0 {
		out[i] = x
		return out
	}
	return append(out, x)
}

func (s *State) mustSession(id SessionID) (*SessionState, error) {
	se := s.session(id)
	if se == nil {
		return nil, fmt.Errorf("no session %s", id)
	}
	return se, nil
}

// SessionSynced puts a session in the store whole: on a restore, and after a resume.
type SessionSynced struct {
	Session SessionState `json:"session"`
}

func (e SessionSynced) Scope() SessionID { return e.Session.ID }

func (e SessionSynced) Apply(s *State) error {
	if e.Session.ID == "" {
		return fmt.Errorf("session has no id")
	}
	if se := s.session(e.Session.ID); se != nil {
		unseen, pui := se.Unseen, se.ProjectUI
		*se = e.Session
		se.Unseen, se.ProjectUI = unseen, pui
		return nil
	}
	s.Sessions = append(s.Sessions, e.Session)
	return nil
}

type SessionRenamed struct {
	ID   SessionID `json:"id"`
	Name string    `json:"name"`
}

func (e SessionRenamed) Scope() SessionID { return e.ID }

func (e SessionRenamed) Apply(s *State) error {
	se, err := s.mustSession(e.ID)
	if err != nil {
		return err
	}
	se.Name = e.Name
	return nil
}

// SessionArchived archives a session, or brings one back. Its threads and transcript stay.
type SessionArchived struct {
	ID       SessionID `json:"id"`
	Archived bool      `json:"archived"`
}

func (e SessionArchived) Scope() SessionID { return e.ID }

func (e SessionArchived) Apply(s *State) error {
	se, err := s.mustSession(e.ID)
	if err != nil {
		return err
	}
	se.Archived = e.Archived
	if e.Archived {
		se.Agent.Running, se.Agent.Working, se.Agent.Background, se.Agent.Processes = false, false, nil, nil
	}
	return nil
}

// ProjectsChanged is a session's branch and projects after they moved: a repo added, Start work, Start next PR.
type ProjectsChanged struct {
	ID       SessionID `json:"id"`
	Branch   string    `json:"branch,omitempty"`
	Projects []Project `json:"projects"`
	Past     []PastPR  `json:"past,omitempty"`
	Started  bool      `json:"started,omitempty"`
}

func (e ProjectsChanged) Scope() SessionID { return e.ID }

func (e ProjectsChanged) Apply(s *State) error {
	se, err := s.mustSession(e.ID)
	if err != nil {
		return err
	}
	se.Branch, se.Projects, se.Past = e.Branch, slices.Clone(e.Projects), slices.Clone(e.Past)
	se.Started = se.Started || e.Started
	return nil
}

// AgentChanged is a session's agent as it is now.
type AgentChanged struct {
	ID    SessionID  `json:"id"`
	Agent AgentState `json:"agent"`
}

func (e AgentChanged) Scope() SessionID { return e.ID }

func (e AgentChanged) Apply(s *State) error {
	se, err := s.mustSession(e.ID)
	if err != nil {
		return err
	}
	se.Agent = e.Agent
	return nil
}

// ThreadUpdated is a thread as it is now: opened, replied to, resolved.
type ThreadUpdated struct {
	Session SessionID `json:"session"`
	Thread  Thread    `json:"thread"`
}

func (e ThreadUpdated) Scope() SessionID { return e.Session }

func (e ThreadUpdated) Apply(s *State) error {
	if e.Thread.ID == "" {
		return fmt.Errorf("thread has no id")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Threads = upsert(se.Threads, e.Thread, func(t Thread) bool { return t.ID == e.Thread.ID })
	return nil
}

// ThreadBusy is a thread going busy, the agent having it, or idle again.
type ThreadBusy struct {
	Session SessionID `json:"session"`
	Thread  ThreadID  `json:"thread"`
	Busy    bool      `json:"busy,omitempty"`
}

func (e ThreadBusy) Scope() SessionID { return e.Session }

func (e ThreadBusy) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(se.Threads, func(t Thread) bool { return t.ID == e.Thread })
	if i < 0 {
		return fmt.Errorf("no thread %s in %s", e.Thread, e.Session)
	}
	se.Threads = slices.Clone(se.Threads)
	se.Threads[i].Busy = e.Busy
	if !e.Busy {
		se.Threads[i].Active = false
	}
	return nil
}

// ThreadsRemoved takes threads out of a session: a plan's, cleared at Start work. The log keeps them.
type ThreadsRemoved struct {
	Session SessionID  `json:"session"`
	Threads []ThreadID `json:"threads"`
}

func (e ThreadsRemoved) Scope() SessionID { return e.Session }

func (e ThreadsRemoved) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Threads = slices.DeleteFunc(slices.Clone(se.Threads), func(t Thread) bool { return slices.Contains(e.Threads, t.ID) })
	return nil
}

// TaskChanged adds a task or changes one. A task changed to "deleted" goes.
type TaskChanged struct {
	Session SessionID `json:"session"`
	Task    Task      `json:"task"`
}

const TaskDeleted TaskStatus = "deleted"

func (e TaskChanged) Scope() SessionID { return e.Session }

func (e TaskChanged) Apply(s *State) error {
	if e.Task.ID == "" {
		return fmt.Errorf("task has no id")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	if e.Task.Status == TaskDeleted {
		se.Tasks = slices.DeleteFunc(slices.Clone(se.Tasks), func(t Task) bool { return t.ID == e.Task.ID })
		return nil
	}
	se.Tasks = upsert(se.Tasks, e.Task, func(t Task) bool { return t.ID == e.Task.ID })
	return nil
}

type SubagentChanged struct {
	Session  SessionID `json:"session"`
	Subagent Subagent  `json:"subagent"`
}

func (e SubagentChanged) Scope() SessionID { return e.Session }

func (e SubagentChanged) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Subagents = upsert(se.Subagents, e.Subagent, func(a Subagent) bool { return a.ID == e.Subagent.ID })
	return nil
}

// AskOpened puts something in front of the user: a permission card, a question, a held connection.
type AskOpened struct {
	Session SessionID `json:"session"`
	Ask     Ask       `json:"ask"`
}

func (e AskOpened) Scope() SessionID { return e.Session }

func (e AskOpened) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Asks = upsert(se.Asks, e.Ask, func(a Ask) bool { return a.ID == e.Ask.ID })
	return nil
}

// AskClosed takes an ask down: answered, timed out or withdrawn. Decision says which.
type AskClosed struct {
	Session  SessionID `json:"session"`
	ID       string    `json:"id"`
	Decision string    `json:"decision,omitempty"` // allow, always, deny, answered, gone
	Note     string    `json:"note,omitempty"`
}

func (e AskClosed) Scope() SessionID { return e.Session }

func (e AskClosed) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Asks = slices.DeleteFunc(slices.Clone(se.Asks), func(a Ask) bool { return a.ID == e.ID })
	return nil
}

// TranscriptAppended adds items to a session's transcript, or replaces those with the same ID: a tool call
// getting its result, prose still streaming.
type TranscriptAppended struct {
	Session SessionID `json:"session"`
	Items   []Item    `json:"items"`
}

func (e TranscriptAppended) Scope() SessionID { return e.Session }

func (e TranscriptAppended) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	// One copy, with room for what's added: older states keep theirs. An item replaced is near the end, so it's
	// looked for from there.
	if e.repeats(se.Transcript) {
		return nil // the transcript as it was: Unchanged says so without comparing it
	}
	out := make([]Item, len(se.Transcript), len(se.Transcript)+len(e.Items))
	copy(out, se.Transcript)
	for _, it := range e.Items {
		if i := LastItem(out, it.ID); it.ID != "" && i >= 0 {
			out[i] = it
			continue
		}
		out = append(out, it)
	}
	if n := len(out) - MaxTranscript; n > 0 {
		out = slices.Clip(out[n:])
	}
	se.Transcript = out
	return nil
}

// repeats says each of e's items is in items already, as it is.
func (e TranscriptAppended) repeats(items []Item) bool {
	for _, it := range e.Items {
		i := LastItem(items, it.ID)
		if it.ID == "" || i < 0 || !reflect.DeepEqual(items[i], it) {
			return false
		}
	}
	return true
}

// Unchanged says the session's transcript is the slice it was: Apply touches nothing else, and leaves it be when its
// items are all there already.
func (e TranscriptAppended) Unchanged(was, next *State) bool {
	a, b := was.session(e.Session), next.session(e.Session)
	if a == nil || b == nil {
		return a == b
	}
	return len(a.Transcript) == len(b.Transcript) && (len(a.Transcript) == 0 || &a.Transcript[0] == &b.Transcript[0])
}

// LastItem is the index of the last item of items with id, -1 if none has it: a transcript's newest first.
func LastItem(items []Item, id string) int {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].ID == id {
			return i
		}
	}
	return -1
}

type TestRunRecorded struct {
	Session SessionID `json:"session"`
	Run     TestRun   `json:"run"`
}

func (e TestRunRecorded) Scope() SessionID { return e.Session }

func (e TestRunRecorded) Apply(s *State) error {
	if e.Run.ID == "" {
		return fmt.Errorf("test run has no id")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Runs = upsert(se.Runs, e.Run, func(r TestRun) bool { return r.ID == e.Run.ID })
	return nil
}

// UsageSampled is a provider's plan usage as last read.
type UsageSampled struct {
	Usage ProviderUsage `json:"usage"`
}

func (e UsageSampled) Scope() SessionID { return "" }

func (e UsageSampled) Apply(s *State) error {
	if e.Usage.Provider == "" {
		return fmt.Errorf("usage has no provider")
	}
	u := e.Usage
	s.Usage = upsert(s.Usage, u, func(o ProviderUsage) bool { return o.Same(u) })
	return nil
}

// UsageRead is a provider's read of its plan windows starting (Reading), or how it went: Failure is why it failed,
// nil when it didn't.
type UsageRead struct {
	Provider string        `json:"provider"`
	Reading  bool          `json:"reading,omitempty"`
	Failure  *UsageFailure `json:"failure,omitempty"`
}

func (e UsageRead) Scope() SessionID { return "" }

func (e UsageRead) Apply(s *State) error {
	if e.Provider == "" {
		return fmt.Errorf("usage read has no provider")
	}
	s.UsageReading = slices.DeleteFunc(slices.Clone(s.UsageReading), func(p string) bool { return p == e.Provider })
	if e.Reading {
		s.UsageReading = append(s.UsageReading, e.Provider)
		return nil
	}
	s.UsageFailures = slices.DeleteFunc(slices.Clone(s.UsageFailures), func(f UsageFailure) bool { return f.Provider == e.Provider })
	if e.Failure != nil {
		f := *e.Failure
		f.Provider = e.Provider
		s.UsageFailures = append(s.UsageFailures, f)
	}
	return nil
}

// MachineChanged is this machine as it is now: its load, its logins.
type MachineChanged struct {
	Machine MachineState `json:"machine"`
}

func (e MachineChanged) Scope() SessionID { return "" }

func (e MachineChanged) Apply(s *State) error {
	s.Machine = e.Machine
	return nil
}

// RuleAdded lets something through from now on in this session: what "Allow for this session" makes of an ask.
type RuleAdded struct {
	Session SessionID `json:"session"`
	Rule    string    `json:"rule"`
}

func (e RuleAdded) Scope() SessionID { return e.Session }

func (e RuleAdded) Apply(s *State) error {
	if e.Rule == "" {
		return fmt.Errorf("empty rule")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	if !slices.Contains(se.Rules, e.Rule) {
		se.Rules = append(slices.Clone(se.Rules), e.Rule)
	}
	return nil
}

// DraftSaved says the agent saved a project's commit message or PR draft. It changes no state, so it's Logged; the
// Ship page reads the drafts again.
type DraftSaved struct {
	Session SessionID `json:"session"`
	Project string    `json:"project"`
	Kind    string    `json:"kind"` // "commit" or "pr"
}

func (e DraftSaved) Scope() SessionID { return e.Session }

func (DraftSaved) Logged() {}

func (e DraftSaved) Apply(s *State) error {
	if e.Kind != "commit" && e.Kind != "pr" {
		return fmt.Errorf("no draft kind %q", e.Kind)
	}
	return nil
}
