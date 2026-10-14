package worker

import (
	"context"
	"encoding/json"
	"io"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker/stats"
)

// What the app calls on a worker.
const (
	MethodInfo      = "worker.info"
	MethodEvents    = "events" // a stream: every session as it is, then each event as it happens
	MethodStart     = "session.start"
	MethodResume    = "session.resume"
	MethodStop      = "session.stop"
	MethodArchive   = "session.archive"
	MethodDelete    = "session.delete"
	MethodNextPR    = "session.nextpr"
	MethodAddRepo   = "session.addrepo"
	MethodRename    = "session.rename"
	MethodStartWork = "session.startwork"
	MethodSearch    = "session.search"
	MethodComment   = "thread.comment"
	MethodThread    = "thread.set"
	MethodAnswer    = "ask.answer"
	MethodSend      = "agent.send"
	MethodInterrupt = "agent.interrupt"
	MethodTerminal  = "agent.terminal" // a stream of the agent's raw terminal
	MethodTermInput = "agent.terminal.input"
	MethodTask      = "task.set"
	MethodRepos     = "repos.list"
	MethodRepoAdd   = "repos.add"
	MethodRepoClone = "repos.clone"
	MethodProviders = "providers.list"
	MethodKinds     = "kinds.list"
	MethodLogAppend = "log.append"
	MethodLogRead   = "log.read"
	MethodRunTests  = "tests.record"
	MethodSessions  = "sessions.list"
	MethodFork      = "agent.fork"
	// MethodSandboxConfig is the sandbox settings, sent on connect and whenever a sandbox.* option changes.
	MethodSandboxConfig = "sandbox.config"
	// MethodStatsQuery answers a stats.Query over the worker's sessions; MethodStatsSession is one session's stats.
	MethodStatsQuery   = "stats.query"
	MethodStatsSession = "stats.session"
	// MethodUsageHistory is the plan readings the worker has kept; MethodUsageRead reads every provider's now.
	MethodUsageHistory = "usage.history"
	MethodUsageRead    = "usage.read"
	// The worker's jobs (sessions.md → Jobs): saved prompts, each run as a session of kind job.
	MethodJobs      = "jobs.list"
	MethodJobSave   = "jobs.save"
	MethodJobDelete = "jobs.delete"
	MethodJobRun    = "jobs.run"
	// MethodTrustSettings trusts a repo's .oriel/settings.toml on the worker's machine, as it is now.
	MethodTrustSettings = "settings.trust"
)

// What a worker calls on the app: tools the app offers agents (LSP lookups), and the policy hooks (layer 2).
const (
	MethodAppTools  = "app.tools"
	MethodAppTool   = "app.tool"
	MethodAppDecide = "app.decide"
)

// Info is what a worker says about itself.
type Info struct {
	ID       core.WorkerID `json:"id"`
	Name     string        `json:"name"`
	Host     string        `json:"host"`
	OS       string        `json:"os"`
	Home     string        `json:"home"`
	Version  string        `json:"version,omitempty"`
	StateDir string        `json:"stateDir"`
}

type SessionRef struct {
	Session core.SessionID `json:"session"`
}

// JobRef names a job.
type JobRef struct {
	Name string `json:"name"`
}

type RenameReq struct {
	Session core.SessionID `json:"session"`
	Name    string         `json:"name"`
}

// StartWorkReq is Start work on a plan, and how; its zero value is the Start work dialog's defaults.
type StartWorkReq struct {
	Session core.SessionID `json:"session"`
	// KeepThreads leaves the plan's threads as they are; otherwise every one but the plan's own goes, the open and
	// deferred ones carried into the work prompt as a note.
	KeepThreads bool `json:"keepThreads,omitempty"`
	// KeepConversation keeps the plan's agent and its context; otherwise a fresh one starts on the work pack.
	KeepConversation bool              `json:"keepConversation,omitempty"`
	Branches         map[string]string `json:"branches,omitempty"` // a project's branch, by its name; its planned one if absent
	Sandbox          *bool             `json:"sandbox,omitempty"`  // nil keeps the session's
	Network          string            `json:"network,omitempty"`  // ask or open; "" keeps the session's
	Model            string            `json:"model,omitempty"`    // a new model runs at Effort, or its default
	Effort           string            `json:"effort,omitempty"`
}

type AddRepoReq struct {
	Session core.SessionID `json:"session"`
	Repo    string         `json:"repo"` // a registered repo's ID, or a path
}

// CommentReq is a comment from the user: on a thread, or opening one (with Anchor, or none for the whole change).
type CommentReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread,omitempty"`
	Anchor  *core.Anchor   `json:"anchor,omitempty"`
	Title   string         `json:"title,omitempty"`
	Body    string         `json:"body"`
	Images  []string       `json:"images,omitempty"`
	Urgent  bool           `json:"urgent,omitempty"` // interrupts the turn
	Btw     bool           `json:"btw,omitempty"`    // a side question, answered by a fork
}

// ThreadReq changes a thread's status: resolve, reopen, defer, dismiss.
type ThreadReq struct {
	Session core.SessionID    `json:"session"`
	Thread  core.ThreadID     `json:"thread"`
	Status  core.ThreadStatus `json:"status,omitempty"`
	Urgent  *bool             `json:"urgent,omitempty"`
}

// AnswerReq answers an ask: allow, always, deny (with a note for the agent), or a question's answers.
type AnswerReq struct {
	Session  core.SessionID    `json:"session"`
	Ask      string            `json:"ask"`
	Decision string            `json:"decision"` // allow, always, deny, answer; recheck for a login
	Note     string            `json:"note,omitempty"`
	Answers  map[string]string `json:"answers,omitempty"`
	Scope    string            `json:"scope,omitempty"` // a network rule's host pattern
}

// SendReq is a message to the agent outside any thread: the console's composer.
type SendReq struct {
	Session core.SessionID `json:"session"`
	Text    string         `json:"text"`
	Images  []string       `json:"images,omitempty"`
	Urgent  bool           `json:"urgent,omitempty"`
}

// ForkReq is a side question for a session's agent, answered from its context outside the conversation.
type ForkReq struct {
	Session core.SessionID `json:"session"`
	Prompt  string         `json:"prompt"`
}

type TaskReq struct {
	Session core.SessionID `json:"session"`
	Task    core.Task      `json:"task"`
}

type TermReq struct {
	Session core.SessionID `json:"session"`
	Data    []byte         `json:"data,omitempty"`
	Cols    int            `json:"cols,omitempty"`
	Rows    int            `json:"rows,omitempty"`
}

type SearchReq struct {
	Query    string `json:"query"`
	Archived bool   `json:"archived,omitempty"` // archived sessions too
}

// Hit is a match in a session's threads or transcript.
type Hit struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread,omitempty"`
	Item    string         `json:"item,omitempty"`
	Text    string         `json:"text"`
}

// ProviderInfo is a provider as the launcher shows it.
type ProviderInfo struct {
	ID      string             `json:"id"`
	Install agent.Install      `json:"install"`
	Models  []agent.Model      `json:"models,omitempty"`
	Usage   core.ProviderUsage `json:"usage,omitzero"`
	Error   string             `json:"error,omitempty"`
}

type LogAppendReq struct {
	Session core.SessionID `json:"session"`
	Record  logs.Record    `json:"record"`
}

// ToolCall is an agent calling a tool the app offers.
type ToolCall struct {
	Session core.SessionID  `json:"session"`
	Tool    string          `json:"tool"`
	Args    json.RawMessage `json:"args"`
}

// Decide asks the app's policy hooks about an ask before it reaches the user (layer 2).
type Decide struct {
	Session core.SessionID `json:"session"`
	Ask     core.Ask       `json:"ask"`
}

// Verdict is what the policy hooks said: allow, deny, or "" to ask the user.
type Verdict struct {
	Decision string `json:"decision,omitempty"`
	Note     string `json:"note,omitempty"`
	By       string `json:"by,omitempty"`
}

// Client is a worker as the app reaches it: over the wire, or in the same process.
type Client struct{ Conn wire.Conn }

func (c Client) Info(ctx context.Context) (Info, error) {
	var out Info
	return out, c.Conn.Call(ctx, MethodInfo, nil, &out)
}

func (c Client) Sessions(ctx context.Context) ([]core.SessionState, error) {
	var out []core.SessionState
	return out, c.Conn.Call(ctx, MethodSessions, nil, &out)
}

func (c Client) Start(ctx context.Context, s core.SessionSpec) (core.SessionID, error) {
	var out core.SessionID
	return out, c.Conn.Call(ctx, MethodStart, s, &out)
}

func (c Client) Resume(ctx context.Context, id core.SessionID) error {
	return c.Conn.Call(ctx, MethodResume, SessionRef{id}, nil)
}

func (c Client) Stop(ctx context.Context, id core.SessionID) error {
	return c.Conn.Call(ctx, MethodStop, SessionRef{id}, nil)
}

func (c Client) Archive(ctx context.Context, id core.SessionID) error {
	return c.Conn.Call(ctx, MethodArchive, SessionRef{id}, nil)
}

// Delete throws a session away: its worktrees, workspace and log.
func (c Client) Delete(ctx context.Context, id core.SessionID) error {
	return c.Conn.Call(ctx, MethodDelete, SessionRef{id}, nil)
}

// Jobs is the worker's jobs, the most recently changed first.
func (c Client) Jobs(ctx context.Context) ([]Job, error) {
	var out []Job
	return out, c.Conn.Call(ctx, MethodJobs, nil, &out)
}

// SaveJob writes a job, a new one when it has no name, and returns it as saved.
func (c Client) SaveJob(ctx context.Context, j Job) (Job, error) {
	var out Job
	return out, c.Conn.Call(ctx, MethodJobSave, j, &out)
}

func (c Client) DeleteJob(ctx context.Context, name string) error {
	return c.Conn.Call(ctx, MethodJobDelete, JobRef{name}, nil)
}

// RunJob starts a run of a job, and returns its session.
func (c Client) RunJob(ctx context.Context, name string) (core.SessionID, error) {
	var out core.SessionID
	return out, c.Conn.Call(ctx, MethodJobRun, JobRef{name}, &out)
}

func (c Client) NextPR(ctx context.Context, id core.SessionID) error {
	return c.Conn.Call(ctx, MethodNextPR, SessionRef{id}, nil)
}

func (c Client) StartWork(ctx context.Context, r StartWorkReq) error {
	return c.Conn.Call(ctx, MethodStartWork, r, nil)
}

// SetSandboxConfig hands the worker the sandbox settings its sandboxes run on.
func (c Client) SetSandboxConfig(ctx context.Context, cfg sandbox.Config) error {
	return c.Conn.Call(ctx, MethodSandboxConfig, cfg, nil)
}

func (c Client) AddRepo(ctx context.Context, id core.SessionID, repo string) error {
	return c.Conn.Call(ctx, MethodAddRepo, AddRepoReq{Session: id, Repo: repo}, nil)
}

func (c Client) Rename(ctx context.Context, id core.SessionID, name string) error {
	return c.Conn.Call(ctx, MethodRename, RenameReq{Session: id, Name: name}, nil)
}

// Comment adds a comment, and returns the thread it went in.
func (c Client) Comment(ctx context.Context, r CommentReq) (core.ThreadID, error) {
	var out core.ThreadID
	return out, c.Conn.Call(ctx, MethodComment, r, &out)
}

func (c Client) SetThread(ctx context.Context, r ThreadReq) error {
	return c.Conn.Call(ctx, MethodThread, r, nil)
}

func (c Client) Answer(ctx context.Context, r AnswerReq) error {
	return c.Conn.Call(ctx, MethodAnswer, r, nil)
}

func (c Client) Send(ctx context.Context, r SendReq) error {
	return c.Conn.Call(ctx, MethodSend, r, nil)
}

// Fork asks the session's agent a tool-less side question: explain this, btw.
func (c Client) Fork(ctx context.Context, id core.SessionID, prompt string) (string, error) {
	var out string
	return out, c.Conn.Call(ctx, MethodFork, ForkReq{Session: id, Prompt: prompt}, &out)
}

func (c Client) Interrupt(ctx context.Context, id core.SessionID) error {
	return c.Conn.Call(ctx, MethodInterrupt, SessionRef{id}, nil)
}

func (c Client) SetTask(ctx context.Context, id core.SessionID, t core.Task) error {
	return c.Conn.Call(ctx, MethodTask, TaskReq{Session: id, Task: t}, nil)
}

func (c Client) Repos(ctx context.Context) ([]core.Repo, error) {
	var out []core.Repo
	return out, c.Conn.Call(ctx, MethodRepos, nil, &out)
}

func (c Client) AddRepoPath(ctx context.Context, path string) (core.Repo, error) {
	var out core.Repo
	return out, c.Conn.Call(ctx, MethodRepoAdd, path, &out)
}

// CloneRepo clones a repo onto the worker and registers it.
func (c Client) CloneRepo(ctx context.Context, r CloneReq) (core.Repo, error) {
	var out core.Repo
	return out, c.Conn.Call(ctx, MethodRepoClone, r, &out)
}

// TrustSettings trusts a registered repo's .oriel/settings.toml on the worker, as it is now.
func (c Client) TrustSettings(ctx context.Context, path string) error {
	return c.Conn.Call(ctx, MethodTrustSettings, path, nil)
}

func (c Client) Providers(ctx context.Context) ([]ProviderInfo, error) {
	var out []ProviderInfo
	return out, c.Conn.Call(ctx, MethodProviders, nil, &out)
}

// Kinds is the kinds of session the worker starts, built-ins and plugins', in order.
func (c Client) Kinds(ctx context.Context) ([]KindSpec, error) {
	var out []KindSpec
	return out, c.Conn.Call(ctx, MethodKinds, nil, &out)
}

// StatsQuery answers q over the worker's sessions (stats.query).
func (c Client) StatsQuery(ctx context.Context, q stats.Query) (stats.Report, error) {
	var out stats.Report
	return out, c.Conn.Call(ctx, MethodStatsQuery, q, &out)
}

// SessionStats is one session's stats (stats.session).
func (c Client) SessionStats(ctx context.Context, r StatsSessionReq) (stats.Detail, error) {
	var out stats.Detail
	return out, c.Conn.Call(ctx, MethodStatsSession, r, &out)
}

// UsageHistory is the plan readings the worker has kept, oldest first.
func (c Client) UsageHistory(ctx context.Context, r UsageHistoryReq) ([]core.ProviderUsage, error) {
	var out []core.ProviderUsage
	return out, c.Conn.Call(ctx, MethodUsageHistory, r, &out)
}

// ReadUsage has the worker read every provider's plan windows now; they arrive as UsageSampled.
func (c Client) ReadUsage(ctx context.Context) error {
	return c.Conn.Call(ctx, MethodUsageRead, nil, nil)
}

func (c Client) Search(ctx context.Context, r SearchReq) ([]Hit, error) {
	var out []Hit
	return out, c.Conn.Call(ctx, MethodSearch, r, &out)
}

func (c Client) RecordRun(ctx context.Context, id core.SessionID, run core.TestRun) error {
	return c.Conn.Call(ctx, MethodRunTests, core.TestRunRecorded{Session: id, Run: run}, nil)
}

// Events streams every session the worker has, as SessionSynced, then every event after, into apply. It
// returns when the stream ends.
func (c Client) Events(ctx context.Context, apply func(core.Event) error) error {
	s, err := c.Conn.Stream(ctx, MethodEvents, nil)
	if err != nil {
		return err
	}
	defer s.Close()
	for {
		var env core.Envelope
		if err := s.Recv(&env); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		ev, err := core.DecodeEvent(env)
		if err != nil {
			continue
		}
		_ = apply(ev)
	}
}

// Terminal streams the agent's raw terminal: what it has shown, then what it shows.
func (c Client) Terminal(ctx context.Context, id core.SessionID) (wire.Stream, error) {
	return c.Conn.Stream(ctx, MethodTerminal, SessionRef{id})
}

func (c Client) TermInput(ctx context.Context, r TermReq) error {
	return c.Conn.Call(ctx, MethodTermInput, r, nil)
}

// Logs is logs.SessionLogs kept on the worker.
func (c Client) Logs() logs.SessionLogs { return remoteLogs{c} }

type remoteLogs struct{ c Client }

func (r remoteLogs) Open(id core.SessionID) (logs.SessionLog, error) { return remoteLog{r.c, id}, nil }

type remoteLog struct {
	c  Client
	id core.SessionID
}

func (l remoteLog) Append(rec logs.Record) error {
	return l.c.Conn.Call(context.Background(), MethodLogAppend, LogAppendReq{Session: l.id, Record: rec}, nil)
}

func (l remoteLog) Records() ([]logs.Record, error) {
	var out []logs.Record
	return out, l.c.Conn.Call(context.Background(), MethodLogRead, SessionRef{l.id}, &out)
}

func (l remoteLog) Close() error { return nil }
