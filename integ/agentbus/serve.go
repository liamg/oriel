package agentbus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/integ/agentbus/autoapprove"
)

// AutoAnswerPrefix starts every answer an agent gives for its absent human.
const AutoAnswerPrefix = "My human was unavailable, but my answer follows:"

// EnvData is where the plugin's process keeps its log: the plugin's data directory, which the host gives it.
const EnvData = "ORIEL_PLUGIN_DATA"

// Server is the plugin's process: it answers the plugin's Lua (Handle) and tells it when anything changed (the
// notification "changed", with a Snapshot).
type Server struct {
	GitHub  GitHub                          // nil: no PR details, auto-approval or GitHub reviews
	Notify  func(method string, params any) // to the Lua
	LogPath string                          // the log's file; in memory if empty
	Every   time.Duration                   // how often requests are checked for GitHub reviews; GitHubEvery if 0

	mu      sync.Mutex
	bus     *Bus
	key     string // the URL and instance the bus runs with
	cancel  context.CancelFunc
	rev     *Reviewer
	rulesE  string
	due     map[string]Item // asks handed out for the agent to answer
	kick    chan struct{}
	started bool
}

// Configure is the plugin's settings, sent whenever they change.
type Configure struct {
	URL      string   `json:"url"`
	Instance string   `json:"instance"` // this harness's id on the bus: "oriel-<host>" if empty
	Label    string   `json:"label"`
	Version  string   `json:"version"`
	Asks     bool     `json:"asks"`
	Reviews  bool     `json:"reviews"`
	Repos    []string `json:"repos"`
	Presence string   `json:"presence"`
	Rules    string   `json:"rules"` // auto-approve rules, as JSON
}

// Snapshot is everything the plugin shows: the connection, the open items, recent auto-approvals and the log's tail.
type Snapshot struct {
	Status     Status         `json:"status"`
	Items      []ItemView     `json:"items"`
	Auto       []AutoApproval `json:"auto"`
	Log        []LogView      `json:"log"`
	RulesError string         `json:"rules_error,omitempty"`
}

// ItemView is an item with its times in seconds, as Lua reads them.
type ItemView struct {
	Item
	CreatedAt  float64 `json:"created_at"`
	DeadlineAt float64 `json:"deadline_at,omitempty"`
}

// LogView is a log entry with its time in seconds.
type LogView struct {
	LogEntry
	AtS float64 `json:"at_s"`
}

// Due is an ask the agent should answer now.
type Due struct {
	ID       string   `json:"id"`
	From     string   `json:"from"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
	Prompt   string   `json:"prompt"`
}

func seconds(t time.Time) float64 { return float64(t.UnixMilli()) / 1000 }

func fromSeconds(s float64) time.Time { return time.UnixMilli(int64(s * 1000)) }

// logTail is how much of the log a snapshot carries.
const logTail = 30

// Snapshot is the state now.
func (s *Server) Snapshot() Snapshot {
	s.mu.Lock()
	b, rulesE := s.bus, s.rulesE
	s.mu.Unlock()
	out := Snapshot{Items: []ItemView{}, Auto: []AutoApproval{}, Log: []LogView{}, RulesError: rulesE}
	if b == nil {
		out.Status.Presence = PresenceActive
		return out
	}
	out.Status, out.Auto = b.Status(), b.AutoApproved()
	for _, it := range b.Items() {
		v := ItemView{Item: it, CreatedAt: seconds(it.Created)}
		if it.Deadline != nil {
			v.DeadlineAt = seconds(*it.Deadline)
		}
		out.Items = append(out.Items, v)
	}
	for _, e := range b.Log(logTail) {
		out.Log = append(out.Log, LogView{LogEntry: e, AtS: seconds(e.At)})
	}
	return out
}

// changed tells the Lua, at most every 50ms.
func (s *Server) changed() {
	s.mu.Lock()
	if s.kick == nil {
		s.kick = make(chan struct{}, 1)
		go func() {
			for range s.kick {
				if s.Notify != nil {
					s.Notify("changed", s.Snapshot())
				}
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}
	kick := s.kick
	s.mu.Unlock()
	select {
	case kick <- struct{}{}:
	default:
	}
}

func (s *Server) configure(c Configure) Snapshot {
	if c.Instance == "" {
		host, _ := os.Hostname()
		c.Instance = "oriel-" + cmpOr(strings.Split(host, ".")[0], "host")
	}
	rules, err := autoapprove.Parse(c.Rules)
	s.mu.Lock()
	s.rulesE = ""
	if err != nil {
		s.rulesE = err.Error()
		rules = nil
	}
	if s.rev == nil && s.GitHub != nil {
		s.rev = &Reviewer{GitHub: s.GitHub}
	}
	if s.rev != nil {
		s.rev.SetRules(rules)
	}
	key := c.URL + "\x00" + c.Instance
	b := s.bus
	if b == nil || key != s.key {
		if s.cancel != nil {
			s.cancel()
		}
		cfg := Config{Instance: Instance{ID: c.Instance, Harness: "oriel", Version: c.Version, Label: c.Label}, URL: c.URL,
			LogPath: s.LogPath, OnChange: s.changed}
		if rev := s.rev; rev != nil {
			cfg.PRInfo = rev.PRInfo
		}
		b = New(cfg)
		if rev := s.rev; rev != nil {
			b.cfg.Vet = func(ctx context.Context, it Item) bool { return rev.Vet(ctx, b, it) }
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.bus, s.key, s.cancel = b, key, cancel
		go b.Run(ctx)
		if rev := s.rev; rev != nil {
			go s.watchGitHub(ctx, rev, b)
		}
	}
	s.mu.Unlock()
	b.SetSettings(context.Background(), Settings{Asks: c.Asks, Reviews: c.Reviews, Repos: c.Repos, Presence: c.Presence})
	return s.Snapshot()
}

func (s *Server) watchGitHub(ctx context.Context, rev *Reviewer, b *Bus) {
	t := time.NewTicker(cmpOr(s.Every, GitHubEvery))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rev.CheckGitHub(ctx, b)
		}
	}
}

// Close stops the bus.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *Server) theBus() (*Bus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bus == nil {
		return nil, errors.New("agentbus isn't configured yet")
	}
	return s.bus, nil
}

// answerPrompt is what the agent is asked, to answer for its absent human.
func answerPrompt(it Item) string {
	var b strings.Builder
	b.WriteString("Someone asked your human a question through agentbus, and your human did not answer in time. ")
	b.WriteString("Answer it for them, briefly and plainly. If you do not know, say so; never guess at facts about their work.\n\n")
	fmt.Fprintf(&b, "From: %s", it.From.User)
	if s := it.From.Session; s != nil && s.Agent != "" {
		fmt.Fprintf(&b, " (via their agent %s)", s.Agent)
	}
	fmt.Fprintf(&b, "\nQuestion: %s\n", it.Ask.Question)
	if len(it.Ask.Options) > 0 {
		fmt.Fprintf(&b, "Options: %s (pick one if it fits, and say which)\n", strings.Join(it.Ask.Options, " / "))
	}
	b.WriteString("\nReply with the answer only.")
	return b.String()
}

// Params are every method's arguments.
type Params struct {
	Configure
	ID      string  `json:"id"`
	Text    string  `json:"text"`
	Option  string  `json:"option"`
	State   string  `json:"state"`
	Summary string  `json:"summary"`
	Link    string  `json:"link"`
	PR      string  `json:"pr"`
	Note    string  `json:"note"`
	To      string  `json:"to"`
	Now     float64 `json:"now"`
	Limit   int     `json:"limit"`
}

// Handle answers one request from the plugin's Lua.
func (s *Server) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p Params
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
	}
	if method == "configure" {
		return s.configure(p.Configure), nil
	}
	if method == "snapshot" {
		return s.Snapshot(), nil
	}
	b, err := s.theBus()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ok := map[string]bool{"ok": true}
	switch method {
	case "answer":
		text := strings.TrimSpace(p.Text)
		if text == "" && p.Option == "" {
			return nil, errors.New("type an answer or pick an option")
		}
		return ok, b.Answer(ctx, p.ID, AnswerBody{Text: text, Option: p.Option, By: AnsweredByHuman})
	case "later", "ignore":
		b.Hide(p.ID, map[string]string{"later": EventLater, "ignore": EventIgnored}[method])
		return ok, nil
	case "show":
		if !b.Show(p.ID) {
			return nil, errors.New("that request is gone")
		}
		return ok, nil
	case "claim":
		return ok, b.Claim(ctx, p.ID)
	case "result":
		return ok, b.Result(ctx, p.ID, ReviewResultBody{State: p.State, Summary: p.Summary, URL: p.Link})
	case "due":
		var out []Due
		for _, it := range b.Due(fromSeconds(p.Now)) {
			if b.FromUser(it) || it.Ask == nil {
				continue
			}
			s.mu.Lock()
			if s.due == nil {
				s.due = map[string]Item{}
			}
			s.due[it.ID] = it
			s.mu.Unlock()
			out = append(out, Due{ID: it.ID, From: it.From.User, Question: it.Ask.Question, Options: it.Ask.Options, Prompt: answerPrompt(it)})
		}
		if out == nil {
			out = []Due{}
		}
		return map[string]any{"items": out}, nil
	case "auto_answer":
		s.mu.Lock()
		it, found := s.due[p.ID]
		delete(s.due, p.ID)
		s.mu.Unlock()
		if !found {
			return nil, fmt.Errorf("no question %s is waiting on the agent", p.ID)
		}
		text := strings.TrimSpace(p.Text)
		if text == "" {
			return nil, errors.New("the agent gave no answer")
		}
		return ok, b.AnswerItem(ctx, it, AnswerBody{Text: AutoAnswerPrefix + "\n\n" + text, By: AnsweredByAgent})
	case "request_review":
		pr, _, err := ParsePR(p.PR)
		if err != nil {
			return nil, err
		}
		id, err := b.RequestReview(ctx, pr, p.Note, strings.TrimSpace(p.To))
		return map[string]string{"id": id}, err
	case "request_state":
		return b.RequestState(ctx, p.ID)
	case "ask":
		if strings.TrimSpace(p.To) == "" || strings.TrimSpace(p.Text) == "" {
			return nil, errors.New("ask needs to and text")
		}
		id, err := b.Ask(ctx, strings.TrimSpace(p.To), strings.TrimSpace(p.Text))
		return map[string]string{"id": id}, err
	case "ask_state":
		return b.AskState(ctx, p.ID)
	case "who":
		rows, err := b.Who(ctx)
		if rows == nil {
			rows = []Online{}
		}
		return map[string]any{"online": rows}, err
	case "log":
		return map[string]any{"entries": b.Log(cmpOr(p.Limit, 500))}, nil
	case "check_github":
		s.mu.Lock()
		rev := s.rev
		s.mu.Unlock()
		if rev == nil {
			return nil, errors.New("no GitHub")
		}
		rev.CheckGitHub(ctx, b)
		return ok, nil
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
}

// Serve answers the plugin's Lua on r and w (JSON-RPC 2.0, a message a line) until r ends. GitHub goes through the
// host's http.request with the GITHUB_TOKEN secret, which the host attaches for api.github.com: the process never
// sees it.
func Serve(r io.Reader, w io.Writer, s *Server) {
	var conn *jsonrpc.Conn
	conn = jsonrpc.NewConn(r, w, jsonrpc.Handlers{
		Request: func(req *jsonrpc.Request) {
			go func() { req.Reply(s.Handle(context.Background(), req.Method, req.Params)) }()
		},
	}, jsonrpc.Options{Version: "2.0"})
	s.Notify = func(method string, params any) { conn.Notify(method, params) }
	if s.GitHub == nil {
		s.GitHub = gh.API{T: HostTransport{Conn: conn}}
	}
	if s.LogPath == "" {
		if dir := os.Getenv(EnvData); dir != "" {
			s.LogPath = filepath.Join(dir, "log.jsonl")
		}
	}
	<-conn.Done()
	s.Close()
}

// GitHubSecret is the secret GitHub's calls carry, bound to api.github.com by the plugin's manifest.
const GitHubSecret = "GITHUB_TOKEN"

// HostTransport is GitHub's REST API through the host's http.request, with the GitHubSecret attached by the host.
type HostTransport struct {
	Conn *jsonrpc.Conn
	Base string // https://api.github.com/ if empty
}

func (t HostTransport) Do(ctx context.Context, method, path string, in, out any) error {
	req := map[string]any{"method": method, "url": strings.TrimSuffix(cmpOr(t.Base, "https://api.github.com/"), "/") + "/" + path,
		"headers": map[string]string{"Accept": "application/vnd.github+json"}, "auth": GitHubSecret}
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		req["body"] = string(b)
		req["headers"].(map[string]string)["Content-Type"] = "application/json"
	}
	var res struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}
	if err := t.Conn.Call(ctx, "http.request", req, &res); err != nil {
		return err
	}
	if res.Status >= 300 {
		var e struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(res.Body), &e) != nil || e.Message == "" {
			e.Message = strings.TrimSpace(res.Body)
		}
		return &gh.Error{Status: res.Status, Message: e.Message}
	}
	switch o := out.(type) {
	case nil:
		return nil
	case *string:
		*o = res.Body
		return nil
	}
	if len(bytes.TrimSpace([]byte(res.Body))) == 0 {
		return nil
	}
	return json.Unmarshal([]byte(res.Body), out)
}

func cmpOr[T comparable](v, d T) T {
	var zero T
	if v == zero {
		return d
	}
	return v
}
