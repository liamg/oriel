package core

import (
	"fmt"
	"slices"
)

func init() {
	RegisterEvent[SessionSynced]("SessionSynced")
	RegisterEvent[SessionRenamed]("SessionRenamed")
	RegisterEvent[SessionArchived]("SessionArchived")
	RegisterEvent[ProjectsChanged]("ProjectsChanged")
	RegisterEvent[AgentChanged]("AgentChanged")
	RegisterEvent[ThreadUpdated]("ThreadUpdated")
	RegisterEvent[TaskChanged]("TaskChanged")
	RegisterEvent[SubagentChanged]("SubagentChanged")
	RegisterEvent[AskOpened]("AskOpened")
	RegisterEvent[AskClosed]("AskClosed")
	RegisterEvent[TranscriptAppended]("TranscriptAppended")
	RegisterEvent[TestRunRecorded]("TestRunRecorded")
	RegisterEvent[UsageSampled]("UsageSampled")
	RegisterEvent[WorkerChanged]("WorkerChanged")
	RegisterEvent[WorkerGone]("WorkerGone")
	RegisterEvent[RuleAdded]("RuleAdded")
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

// SessionSynced puts a session in the store whole, as its worker has it: on connect, and after a resume.
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
		se.Agent.Running, se.Agent.Working = false, false
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
	out := slices.Clone(se.Transcript)
	for _, it := range e.Items {
		if i := slices.IndexFunc(out, func(o Item) bool { return o.ID == it.ID }); it.ID != "" && i >= 0 {
			out[i] = it
			continue
		}
		out = append(out, it)
	}
	if n := len(out) - MaxTranscript; n > 0 {
		out = slices.Clone(out[n:])
	}
	se.Transcript = out
	return nil
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
	s.Usage = upsert(s.Usage, e.Usage, func(u ProviderUsage) bool { return u.Provider == e.Usage.Provider })
	return nil
}

// WorkerChanged is a worker as it is now: connected, its load.
type WorkerChanged struct {
	Worker WorkerState `json:"worker"`
}

func (e WorkerChanged) Scope() SessionID { return "" }

func (e WorkerChanged) Apply(s *State) error {
	if e.Worker.ID == "" {
		return fmt.Errorf("worker has no id")
	}
	s.Workers = upsert(s.Workers, e.Worker, func(w WorkerState) bool { return w.ID == e.Worker.ID })
	return nil
}

// WorkerGone is a worker that disconnected: its sessions leave the store until it's back.
type WorkerGone struct {
	ID WorkerID `json:"id"`
}

func (e WorkerGone) Scope() SessionID { return "" }

func (e WorkerGone) Apply(s *State) error {
	i := slices.IndexFunc(s.Workers, func(w WorkerState) bool { return w.ID == e.ID })
	if i < 0 {
		return fmt.Errorf("no worker %s", e.ID)
	}
	s.Workers = slices.Clone(s.Workers)
	s.Workers[i].Connected = false
	s.Sessions = slices.DeleteFunc(slices.Clone(s.Sessions), func(se SessionState) bool { return se.Worker == e.ID })
	if s.session(s.Focus) == nil {
		s.Focus = ""
	}
	return nil
}

// RuleAdded lets something through from now on in this session: what "Always" makes of an ask.
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
