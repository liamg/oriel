// Package rpcagent is the Oriel agent protocol: the whole of agent.Provider and agent.Session over JSON-RPC 2.0,
// one message a line, so a process plugin can bring an agent with nothing lost against one built in. Client is
// the Oriel side, a connection as an agent.Provider; Serve is the plugin side, an agent.Provider on a connection.
// docs/agent-protocol.md describes it for plugin authors.
package rpcagent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
)

// Version is the protocol's version, which initialize agrees.
const Version = 1

// Errors past JSON-RPC's own, each with its data.
const (
	CodeNoFork      = -32021 // agent.ErrNoFork
	CodeAuthExpired = -32022 // an agent.AuthExpired, as data
	CodeUsageProbe  = -32023 // an agent.UsageProbeFailed: data {provider, started}
	CodeExec        = -32024 // a process that can't be run here
)

// CancelRequest is the notification that cancels a request whose caller stopped waiting: {id}.
func CancelRequest() *jsonrpc.Cancel { return &jsonrpc.Cancel{Method: "$/cancelRequest", Field: "id"} }

// Init is initialize's params: who Oriel calls the provider, and the config it was registered with.
type Init struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Name            string          `json:"name"`
	Config          json.RawMessage `json:"config,omitempty"`
}

// Hello is initialize's result: the provider's id and what it can do.
type Hello struct {
	ProtocolVersion int      `json:"protocolVersion"`
	ID              string   `json:"id"`
	Features        Features `json:"features"`
	Logged          bool     `json:"logged,omitempty"`     // it answers logs (agent.Logged)
	Discovered      bool     `json:"discovered,omitempty"` // it answers discover (agent.Discovered)
	// MCP asks Oriel to serve each session's tools as an MCP stdio server, which session/start names.
	MCP bool `json:"mcp,omitempty"`
}

// Features are agent.Features on the wire.
type Features struct {
	Terminal bool   `json:"terminal,omitempty"`
	Fork     bool   `json:"fork,omitempty"`
	Images   bool   `json:"images,omitempty"`
	Sandbox  bool   `json:"sandbox,omitempty"`
	Login    bool   `json:"login,omitempty"`
	Notes    string `json:"notes,omitempty"` // the file it reads standing instructions from: AGENTS.md if ""
	// StopProcesses says its sessions answer session/stopProcess.
	StopProcesses bool `json:"stopProcesses,omitempty"`
}

// FeaturesOf are f on the wire.
func FeaturesOf(f agent.Features) Features {
	return Features{Terminal: f.Terminal, Fork: f.Fork, Images: f.Images, Sandbox: f.Sandbox, Login: f.Login, Notes: f.Notes,
		StopProcesses: f.StopProcesses}
}

// Agent are the features as Oriel's.
func (f Features) Agent() agent.Features {
	return agent.Features{Terminal: f.Terminal, Fork: f.Fork, Images: f.Images, Sandbox: f.Sandbox, Login: f.Login, Notes: f.Notes,
		StopProcesses: f.StopProcesses}
}

type install struct {
	Bin      string `json:"bin,omitempty"`
	Version  string `json:"version,omitempty"`
	LoggedIn bool   `json:"loggedIn,omitempty"`
	Login    string `json:"login,omitempty"`
	Account  string `json:"account,omitempty"`
	Note     string `json:"note,omitempty"`
}

func installOf(in agent.Install) install {
	return install{Bin: in.Bin, Version: in.Version, LoggedIn: in.LoggedIn, Login: in.Login, Account: in.Account, Note: in.Note}
}

func (w install) install() agent.Install {
	return agent.Install{Bin: w.Bin, Version: w.Version, LoggedIn: w.LoggedIn, Login: w.Login, Account: w.Account, Note: w.Note}
}

type mount struct {
	Host     string `json:"host"`
	Sandbox  string `json:"sandbox,omitempty"`
	ReadOnly bool   `json:"readOnly,omitempty"`
}

func mountsOf(ms []agent.Mount) []mount {
	out := []mount{}
	for _, m := range ms {
		out = append(out, mount{Host: m.Host, Sandbox: m.Sandbox, ReadOnly: m.ReadOnly})
	}
	return out
}

func agentMounts(ms []mount) []agent.Mount {
	var out []agent.Mount
	for _, m := range ms {
		out = append(out, agent.Mount{Host: m.Host, Sandbox: m.Sandbox, ReadOnly: m.ReadOnly})
	}
	return out
}

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Schema      json.RawMessage `json:"inputSchema,omitempty"`
	ReadOnly    bool            `json:"readOnly,omitempty"`
}

type pack struct {
	Name  string   `json:"name,omitempty"`
	Skill string   `json:"skill,omitempty"`
	Text  string   `json:"text,omitempty"`
	Tools []string `json:"tools,omitempty"`
}

type sandbox struct {
	Name       string `json:"name"`
	HookSocket string `json:"hookSocket,omitempty"` // the hook socket's path inside the sandbox
	GatewayMCP string `json:"gatewayMCP,omitempty"` // the host's file
	// Gateway is that file's contents, for a plugin that can't read it.
	Gateway json.RawMessage `json:"gateway,omitempty"`
}

// stdioServer is an MCP stdio server: how to start it.
type stdioServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Socket  string            `json:"socket"` // the host's socket, which ORIEL_HOOK_SOCK names
}

// startSpec is agent.StartSpec on the wire: its Tools are called back with session/tool, and its Exec is exec/*.
type startSpec struct {
	Cwd      string       `json:"cwd,omitempty"`
	Model    string       `json:"model,omitempty"`
	Effort   string       `json:"effort,omitempty"`
	Pack     pack         `json:"pack"`
	Prompt   string       `json:"prompt,omitempty"`
	Tools    []tool       `json:"tools"`
	Policy   agent.Policy `json:"policy,omitempty"`
	Protect  []string     `json:"protect,omitempty"`
	Env      []string     `json:"env,omitempty"`
	Sandbox  *sandbox     `json:"sandbox,omitempty"`
	StateDir string       `json:"stateDir,omitempty"`
	Memory   string       `json:"memory,omitempty"`
	NoMCP    bool         `json:"noMCP,omitempty"`
	MCP      *stdioServer `json:"mcp,omitempty"`
}

type startParams struct {
	Session string    `json:"session"`
	Resume  string    `json:"resume,omitempty"`
	Spec    startSpec `json:"spec"`
}

type startResult struct {
	ID       string `json:"id"`
	Terminal bool   `json:"terminal,omitempty"`
}

type input struct {
	Text   string   `json:"text"`
	Images []string `json:"images,omitempty"`
	Urgent bool     `json:"urgent,omitempty"`
}

type decision struct {
	Allow   bool              `json:"allow,omitempty"`
	Always  bool              `json:"always,omitempty"`
	Note    string            `json:"note,omitempty"`
	Answers map[string]string `json:"answers,omitempty"`
}

// sessionParams address a session, and say what's asked of it.
type sessionParams struct {
	Session  string    `json:"session"`
	Input    *input    `json:"input,omitempty"`
	Prompt   string    `json:"prompt,omitempty"`   // session/fork
	ID       string    `json:"id,omitempty"`       // session/answer
	Decision *decision `json:"decision,omitempty"` // session/answer
	Data     string    `json:"data,omitempty"`     // terminal/write, terminal/output: base64
	Cols     int       `json:"cols,omitempty"`     // terminal/resize
	Rows     int       `json:"rows,omitempty"`
	Process  string    `json:"process,omitempty"` // session/stopProcess
}

type toolParams struct {
	Session   string          `json:"session"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Output string `json:"output"`
}

type forkResult struct {
	Answer string `json:"answer"`
}

// execParams start a process for a session, or act on one.
type execParams struct {
	Session string   `json:"session,omitempty"`
	Process string   `json:"process"`
	Path    string   `json:"path,omitempty"` // exec/start: the program, as the plugin found it
	Args    []string `json:"args,omitempty"` // exec/start: argv, its name first
	Dir     string   `json:"dir,omitempty"`
	Env     []string `json:"env,omitempty"` // nil inherits Oriel's
	Stderr  bool     `json:"stderr,omitempty"`
	PTY     *size    `json:"pty,omitempty"` // a terminal of this size; pipes when nil
	Data    string   `json:"data,omitempty"`
	Cols    int      `json:"cols,omitempty"`
	Rows    int      `json:"rows,omitempty"`
}

type size struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// output is a process's output: stream is "stdout", "stderr" or "pty".
type output struct {
	Process string `json:"process"`
	Stream  string `json:"stream"`
	Data    string `json:"data"`
}

type exit struct {
	Process string `json:"process"`
	Error   string `json:"error,omitempty"`
	Code    int    `json:"code,omitempty"`
}

type loginParams struct {
	Login string `json:"login"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
	Done  bool   `json:"done,omitempty"`
}

type place struct {
	Call     string   `json:"call"`
	Root     string   `json:"root,omitempty"`
	Cwd      string   `json:"cwd,omitempty"`
	Dir      string   `json:"dir,omitempty"`
	Home     string   `json:"home,omitempty"`
	Tmp      string   `json:"tmp,omitempty"`
	OS       string   `json:"os,omitempty"`
	Arch     string   `json:"arch,omitempty"`
	Shared   bool     `json:"shared,omitempty"`
	UID      int      `json:"uid,omitempty"`
	StateDir string   `json:"stateDir,omitempty"`
	Environ  []string `json:"environ,omitempty"`
	Cache    string   `json:"cache,omitempty"`
}

func placeOf(call string, p agent.SandboxPlace) place {
	return place{Call: call, Root: p.Root, Cwd: p.Cwd, Dir: p.Dir, Home: p.Home, Tmp: p.Tmp, OS: p.OS, Arch: p.Arch, Shared: p.Shared,
		UID: p.UID, StateDir: p.StateDir, Environ: p.Environ, Cache: p.Cache}
}

func (w place) place() agent.SandboxPlace {
	return agent.SandboxPlace{Root: w.Root, Cwd: w.Cwd, Dir: w.Dir, Home: w.Home, Tmp: w.Tmp, OS: w.OS, Arch: w.Arch, Shared: w.Shared,
		UID: w.UID, StateDir: w.StateDir, Environ: w.Environ, Cache: w.Cache}
}

// placeCall is the plugin asking about a sandbox/needs call under way: sandbox/report {call, step, note} and
// sandbox/env {call, name} → {value}.
type placeCall struct {
	Call  string `json:"call"`
	Step  string `json:"step,omitempty"`
	Note  string `json:"note,omitempty"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
}

type problem struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Blocking bool     `json:"blocking,omitempty"`
	SignIn   []string `json:"signIn,omitempty"`
}

type needs struct {
	Bins     map[string]string `json:"bins,omitempty"`
	Tools    []string          `json:"tools,omitempty"`
	Mounts   []mount           `json:"mounts,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	PassEnv  []string          `json:"passEnv,omitempty"`
	SkipEnv  []string          `json:"skipEnv,omitempty"`
	URLEnv   []string          `json:"urlEnv,omitempty"`
	URLs     []string          `json:"urls,omitempty"`
	Hosts    []string          `json:"hosts,omitempty"`
	Problems []problem         `json:"problems,omitempty"`
}

func needsOf(n agent.SandboxNeeds) needs {
	w := needs{Bins: n.Bins, Tools: n.Tools, Env: n.Env, PassEnv: n.PassEnv, SkipEnv: n.SkipEnv, URLEnv: n.URLEnv, URLs: n.URLs, Hosts: n.Hosts}
	if len(n.Mounts) > 0 {
		w.Mounts = mountsOf(n.Mounts)
	}
	for _, p := range n.Problems {
		w.Problems = append(w.Problems, problem{Code: p.Code, Message: p.Message, Fix: p.Fix, Blocking: p.Blocking, SignIn: p.SignIn})
	}
	return w
}

func (w needs) needs() agent.SandboxNeeds {
	n := agent.SandboxNeeds{Bins: w.Bins, Tools: w.Tools, Mounts: agentMounts(w.Mounts), Env: w.Env, PassEnv: w.PassEnv, SkipEnv: w.SkipEnv,
		URLEnv: w.URLEnv, URLs: w.URLs, Hosts: w.Hosts}
	for _, p := range w.Problems {
		n.Problems = append(n.Problems, agent.SandboxProblem{Code: p.Code, Message: p.Message, Fix: p.Fix, Blocking: p.Blocking, SignIn: p.SignIn})
	}
	return n
}

type mcpServer struct {
	Name   string            `json:"name"`
	Source string            `json:"source,omitempty"`
	Config json.RawMessage   `json:"config"`
	Vars   map[string]string `json:"vars,omitempty"`
}

type logsParams struct {
	Cwd     string `json:"cwd"`
	Session string `json:"session"`
}

type log struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

type transcript struct {
	Session  string    `json:"session"`
	Logs     []log     `json:"logs"`
	Modified time.Time `json:"modified"`
}

func logsOf(ls []agent.Log) []log {
	out := []log{}
	for _, l := range ls {
		out = append(out, log{Path: l.Path, Format: l.Format})
	}
	return out
}

func agentLogs(ls []log) []agent.Log {
	var out []agent.Log
	for _, l := range ls {
		out = append(out, agent.Log{Path: l.Path, Format: l.Format})
	}
	return out
}

// event is an agent.Event on the wire: its type, and the fields of that type that are set.
type event struct {
	Type        string                `json:"type"`
	ID          string                `json:"id,omitempty"`
	Model       string                `json:"model,omitempty"` // ready
	Role        core.ItemKind         `json:"role,omitempty"`
	Text        string                `json:"text,omitempty"`
	Partial     bool                  `json:"partial,omitempty"`
	Agent       string                `json:"agent,omitempty"`
	Tool        string                `json:"tool,omitempty"`
	Input       json.RawMessage       `json:"input,omitempty"`
	Output      string                `json:"output,omitempty"`
	IsError     bool                  `json:"isError,omitempty"`
	Diff        *core.FileDiff        `json:"diff,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Rule        string                `json:"rule,omitempty"`
	Questions   []core.Question       `json:"questions,omitempty"`
	Description string                `json:"description,omitempty"`
	Kind        string                `json:"kind,omitempty"` // a subagent's type
	Prompt      string                `json:"prompt,omitempty"`
	Done        bool                  `json:"done,omitempty"`
	Result      string                `json:"result,omitempty"`
	Usage       *usage                `json:"usage,omitempty"`
	Background  []core.BackgroundTask `json:"background,omitempty"`
	Running     []core.BackgroundTask `json:"running,omitempty"` // turnEnd
	Error       string                `json:"error,omitempty"`
	Retryable   bool                  `json:"retryable,omitempty"`
	Plan        *core.ProviderUsage   `json:"plan,omitempty"` // usage: the plan's windows
	Provider    string                `json:"provider,omitempty"`
	Account     string                `json:"account,omitempty"`
	Detail      string                `json:"detail,omitempty"`
	Terminal    string                `json:"terminal,omitempty"`
	Shell       string                `json:"shell,omitempty"`
	HostCommand []string              `json:"hostCommand,omitempty"`
}

type usage struct {
	Input      int64   `json:"input,omitempty"`
	Output     int64   `json:"output,omitempty"`
	CacheRead  int64   `json:"cacheRead,omitempty"`
	CacheWrite int64   `json:"cacheWrite,omitempty"`
	Context    int     `json:"context,omitempty"`
	ContextMax int     `json:"contextMax,omitempty"`
	Cost       float64 `json:"cost,omitempty"`
	Duration   int64   `json:"durationMs,omitempty"`
}

func usageOf(u agent.Usage) *usage {
	if u == (agent.Usage{}) {
		return nil
	}
	return &usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Context: u.Context,
		ContextMax: u.ContextMax, Cost: u.Cost, Duration: u.Duration.Milliseconds()}
}

func (u *usage) usage() agent.Usage {
	if u == nil {
		return agent.Usage{}
	}
	return agent.Usage{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Context: u.Context,
		ContextMax: u.ContextMax, Cost: u.Cost, Duration: time.Duration(u.Duration) * time.Millisecond}
}

// eventOf is ev on the wire.
func eventOf(ev agent.Event) (event, error) {
	switch e := ev.(type) {
	case agent.Ready:
		return event{Type: "ready", ID: e.ID, Model: e.Model}, nil
	case agent.TurnStart:
		return event{Type: "turnStart"}, nil
	case agent.Message:
		return event{Type: "message", ID: e.ID, Role: e.Role, Text: e.Text, Partial: e.Partial, Agent: e.Agent}, nil
	case agent.ToolCall:
		return event{Type: "toolCall", ID: e.ID, Tool: e.Tool, Input: e.Input, Agent: e.Agent}, nil
	case agent.ToolResult:
		return event{Type: "toolResult", ID: e.ID, Output: e.Output, IsError: e.IsError, Diff: e.Diff}, nil
	case agent.Permission:
		return event{Type: "permission", ID: e.ID, Tool: e.Tool, Summary: e.Summary, Input: e.Input, Rule: e.Rule}, nil
	case agent.Question:
		return event{Type: "question", ID: e.ID, Questions: e.Questions}, nil
	case agent.Subagent:
		return event{Type: "subagent", ID: e.ID, Description: e.Description, Kind: e.Type, Prompt: e.Prompt, Done: e.Done, Result: e.Result}, nil
	case agent.TurnEnd:
		return event{Type: "turnEnd", Usage: usageOf(e.Usage), Background: e.Background, Running: e.Running}, nil
	case agent.Failure:
		return event{Type: "failure", Error: e.Err, Retryable: e.Retryable}, nil
	case agent.UsageUpdate:
		u := e.Usage
		return event{Type: "usage", Plan: &u}, nil
	case agent.AuthExpired:
		return event{Type: "authExpired", Provider: e.Provider, Account: e.Account, Detail: e.Detail, Terminal: e.Terminal, Shell: e.Shell,
			HostCommand: e.HostCommand}, nil
	case agent.AuthRestored:
		return event{Type: "authRestored", Provider: e.Provider, Account: e.Account}, nil
	case agent.Exited:
		return event{Type: "exited", Error: e.Err}, nil
	}
	return event{}, fmt.Errorf("rpcagent: no wire form for %T", ev)
}

// agentEvent is w as an agent.Event; provider is the client's id, which the provider's own stands for.
func (w event) agentEvent() (agent.Event, error) {
	switch w.Type {
	case "ready":
		return agent.Ready{ID: w.ID, Model: w.Model}, nil
	case "turnStart":
		return agent.TurnStart{}, nil
	case "message":
		return agent.Message{ID: w.ID, Role: w.Role, Text: w.Text, Partial: w.Partial, Agent: w.Agent}, nil
	case "toolCall":
		return agent.ToolCall{ID: w.ID, Tool: w.Tool, Input: w.Input, Agent: w.Agent}, nil
	case "toolResult":
		return agent.ToolResult{ID: w.ID, Output: w.Output, IsError: w.IsError, Diff: w.Diff}, nil
	case "permission":
		return agent.Permission{ID: w.ID, Tool: w.Tool, Summary: w.Summary, Input: w.Input, Rule: w.Rule}, nil
	case "question":
		return agent.Question{ID: w.ID, Questions: w.Questions}, nil
	case "subagent":
		return agent.Subagent{ID: w.ID, Description: w.Description, Type: w.Kind, Prompt: w.Prompt, Done: w.Done, Result: w.Result}, nil
	case "turnEnd":
		return agent.TurnEnd{Usage: w.Usage.usage(), Background: w.Background, Running: w.Running}, nil
	case "failure":
		return agent.Failure{Err: w.Error, Retryable: w.Retryable}, nil
	case "usage":
		var u core.ProviderUsage
		if w.Plan != nil {
			u = *w.Plan
		}
		return agent.UsageUpdate{Usage: u}, nil
	case "authExpired":
		return agent.AuthExpired{Provider: w.Provider, Account: w.Account, Detail: w.Detail, Terminal: w.Terminal, Shell: w.Shell,
			HostCommand: w.HostCommand}, nil
	case "authRestored":
		return agent.AuthRestored{Provider: w.Provider, Account: w.Account}, nil
	case "exited":
		return agent.Exited{Err: w.Error}, nil
	}
	return nil, fmt.Errorf("rpcagent: unknown event %q", w.Type)
}

type eventParams struct {
	Session string `json:"session"`
	Event   event  `json:"event"`
}

type probeData struct {
	Provider string `json:"provider"`
	Started  bool   `json:"started,omitempty"`
}

// WireError is err as a reply's error, keeping what Oriel tells apart: no fork, a lost login, a usage probe.
func WireError(err error) error {
	if err == nil {
		return nil
	}
	var je *jsonrpc.Error
	if errors.As(err, &je) {
		return je
	}
	if errors.Is(err, agent.ErrNoFork) {
		return &jsonrpc.Error{Code: CodeNoFork, Message: err.Error()}
	}
	var ae agent.AuthExpired
	if errors.As(err, &ae) {
		w, _ := eventOf(ae)
		b, _ := json.Marshal(w)
		return &jsonrpc.Error{Code: CodeAuthExpired, Message: err.Error(), Data: b}
	}
	var pf agent.UsageProbeFailed
	if errors.As(err, &pf) {
		b, _ := json.Marshal(probeData{Provider: pf.Provider(), Started: pf.Started()})
		return &jsonrpc.Error{Code: CodeUsageProbe, Message: err.Error(), Data: b}
	}
	return &jsonrpc.Error{Code: jsonrpc.InternalError, Message: err.Error()}
}

// localError is a reply's error as the Go error it stood for.
func localError(err error) error {
	var je *jsonrpc.Error
	if !errors.As(err, &je) {
		return err
	}
	switch je.Code {
	case CodeNoFork:
		return agent.ErrNoFork
	case CodeAuthExpired:
		var w event
		if json.Unmarshal(je.Data, &w) == nil {
			if ev, err := w.agentEvent(); err == nil {
				if ae, ok := ev.(agent.AuthExpired); ok {
					return ae
				}
			}
		}
	case CodeUsageProbe:
		var d probeData
		json.Unmarshal(je.Data, &d)
		return probeFailed{msg: je.Message, provider: d.Provider, started: d.Started}
	}
	return errors.New(je.Message)
}

// probeFailed is an agent.UsageProbeFailed that crossed the wire.
type probeFailed struct {
	msg, provider string
	started       bool
}

func (p probeFailed) Error() string    { return p.msg }
func (p probeFailed) Provider() string { return p.provider }
func (p probeFailed) Started() bool    { return p.started }

func b64(p []byte) string { return base64.StdEncoding.EncodeToString(p) }

func unb64(s string) []byte {
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
}
