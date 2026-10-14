package gateway

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/liamg/oriel/sandbox/mcp"
)

// Builtin is one of oriel's own host actions.
type Builtin struct {
	Tool mcp.Tool
	// AlwaysAsk puts every call in front of the user, whatever the rules:
	// the call is a question by nature.
	AlwaysAsk bool
	// Direct skips rules and cards: Run does its own asking.
	Direct bool
	Run    func(ctx context.Context, args json.RawMessage) (mcp.CallResult, error)
}

// Request is a call waiting on the user.
type Request struct {
	Tool        string          `json:"tool"`
	Server      string          `json:"server"`
	Description string          `json:"description,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
	// Siblings are the server's other tools, for the card's checklist.
	Siblings  []string `json:"siblings,omitempty"`
	ReadOnly  bool     `json:"readOnly,omitempty"`
	AlwaysAsk bool     `json:"alwaysAsk,omitempty"`
}

// The decisions a card returns.
const (
	Once    = "once"
	Session = "session"
	Refuse  = "deny"
)

// Answer is the user's decision on a Request.
type Answer struct {
	Decision string `json:"decision"`
	// Tools are granted for the session along with the call.
	Tools []string `json:"tools,omitempty"`
	// Server grants every tool on the call's server for the session.
	Server bool `json:"server,omitempty"`
	// Always also allows Tools (or the server) for good on this machine;
	// Everywhere, on every machine.
	Always     bool   `json:"always,omitempty"`
	Everywhere bool   `json:"everywhere,omitempty"`
	Note       string `json:"note,omitempty"`
	By         string `json:"by,omitempty"` // who refused: "the user" when ""
	// Run is the user pressing Run on host on an ask_user_to_run card.
	Run bool `json:"run,omitempty"`
}

// Entry is one call, for the session's log.
type Entry struct {
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`
	Decision string    `json:"decision"`
	Error    string    `json:"error,omitempty"`
}

// Gateway serves one session's calls.
type Gateway struct {
	Builtins map[string]Builtin
	Up       *Upstreams
	// Levels are the rules in force right now.
	Levels func() Levels
	// Approve asks the user; it returns when they answer or ctx ends.
	Approve func(ctx context.Context, r Request) (Answer, error)
	// Grant records session grants; Always records lasting ones, for every
	// machine when everywhere is set.
	Grant  func(rules []Rule)
	Always func(rules []Rule, everywhere bool)
	Log    func(Entry)
	// Timeout bounds the wait on the user. Zero means ten minutes.
	Timeout time.Duration
}

// Tools lists every tool the session may ask for; denied ones are left out.
func (g *Gateway) Tools(ctx context.Context) ([]mcp.Tool, error) {
	levels := g.levels()
	var out []mcp.Tool
	for _, name := range sortedKeys(g.Builtins) {
		b := g.Builtins[name]
		if b.Direct || Decide(name, false, levels) != Deny {
			out = append(out, b.Tool)
		}
	}
	if g.Up != nil {
		tools, _ := g.Up.Tools(ctx)
		for _, t := range tools {
			if Decide(t.Name, readOnly(t.Tool), levels) != Deny {
				out = append(out, t.Tool)
			}
		}
	}
	return out, nil
}

// ToolPolicy is one tool and what a call to it would meet.
type ToolPolicy struct {
	Name        string `json:"name"`
	Server      string `json:"server"`
	Description string `json:"description,omitempty"`
	Builtin     bool   `json:"builtin,omitempty"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	// Fixed tools take no rule: one that asks by nature, or does its own asking.
	Fixed bool `json:"fixed,omitempty"`
	Why
}

// Policy lists every tool, the denied ones included, with the decision a
// call would get and where it comes from.
func (g *Gateway) Policy(ctx context.Context) []ToolPolicy {
	levels := g.levels()
	var out []ToolPolicy
	for _, name := range sortedKeys(g.Builtins) {
		b := g.Builtins[name]
		p := ToolPolicy{Name: name, Server: ServerOf(name), Description: b.Tool.Description, Builtin: true, Why: Explain(name, false, levels)}
		if b.Direct || (b.AlwaysAsk && p.Action != Deny) {
			p.Fixed, p.Why = true, Why{Action: Ask, From: FromDefault}
		}
		out = append(out, p)
	}
	if g.Up != nil {
		tools, _ := g.Up.Tools(ctx)
		for _, t := range tools {
			ro := readOnly(t.Tool)
			out = append(out, ToolPolicy{Name: t.Name, Server: t.Server, Description: t.Description, ReadOnly: ro, Why: Explain(t.Name, ro, levels)})
		}
	}
	return out
}

// Call runs name once policy, or the user, allows it.
func (g *Gateway) Call(ctx context.Context, name string, args json.RawMessage) (res mcp.CallResult, err error) {
	entry := Entry{At: time.Now().UTC(), Tool: name}
	defer func() {
		if err != nil {
			entry.Error = err.Error()
		} else if res.IsError && entry.Decision != Refuse {
			entry.Error = res.Text()
		}
		if g.Log != nil {
			g.Log(entry)
		}
	}()

	req := Request{Tool: name, Server: ServerOf(name), Args: args}
	var up *Tool
	b, builtin := g.Builtins[name]
	if builtin {
		req.Description = b.Tool.Description
		req.AlwaysAsk = b.AlwaysAsk
		req.Siblings = sortedKeys(g.Builtins)
	} else {
		tools, _ := g.Up.Tools(ctx)
		for i, t := range tools {
			if t.Name == name {
				up = &tools[i]
			}
			if t.Server == req.Server {
				req.Siblings = append(req.Siblings, t.Name)
			}
		}
		if up == nil {
			entry.Decision = Refuse
			return mcp.TextResult("oriel: no tool called "+name, true), nil
		}
		req.Description = up.Description
		req.ReadOnly = readOnly(up.Tool)
	}

	if builtin && b.Direct {
		entry.Decision = "direct"
		return b.Run(ctx, args)
	}
	action := Decide(name, req.ReadOnly, g.levels())
	if req.AlwaysAsk && action != Deny {
		action = Ask
	}
	switch action {
	case Deny:
		entry.Decision = Refuse
		return mcp.TextResult("oriel: "+name+" is denied by the user's settings.", true), nil
	case Ask:
		ans, err := g.ask(ctx, req)
		if err != nil {
			entry.Decision = Refuse
			return mcp.TextResult("oriel: nobody answered the request to use "+name+", so it was denied.", true), nil
		}
		entry.Decision = ans.Decision
		if ans.Decision != Once && ans.Decision != Session {
			entry.Decision = Refuse
			msg := "oriel: " + cmp.Or(ans.By, "the user") + " denied " + name + "."
			if n := strings.TrimSpace(ans.Note); n != "" {
				msg += " They said: " + n
			}
			return mcp.TextResult(msg, true), nil
		}
		g.remember(req, ans)
		ctx = context.WithValue(ctx, answerKey{}, ans)
	default:
		entry.Decision = Allow
	}
	if builtin {
		return b.Run(ctx, args)
	}
	return g.Up.Call(ctx, *up, args)
}

type answerKey struct{}

// AnswerFrom is the user's answer to the card that let this call through, for
// a builtin whose result is that answer.
func AnswerFrom(ctx context.Context) (Answer, bool) {
	a, ok := ctx.Value(answerKey{}).(Answer)
	return a, ok
}

func (g *Gateway) ask(ctx context.Context, req Request) (Answer, error) {
	if g.Approve == nil {
		return Answer{}, fmt.Errorf("no way to ask")
	}
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return g.Approve(ctx, req)
}

// remember turns a session or always answer into rules.
func (g *Gateway) remember(req Request, ans Answer) {
	if ans.Everywhere {
		ans.Always = true
	}
	if ans.Decision != Session && !ans.Always {
		return
	}
	var rules []Rule
	if ans.Server && req.Server != "" {
		rules = append(rules, Rule{Tool: req.Server + "__*", Action: Allow})
	} else {
		tools := append([]string{req.Tool}, ans.Tools...)
		sort.Strings(tools)
		for i, t := range tools {
			if i > 0 && tools[i-1] == t {
				continue
			}
			rules = append(rules, Rule{Tool: t, Action: Allow})
		}
	}
	if req.AlwaysAsk {
		return
	}
	if ans.Decision == Session && g.Grant != nil {
		g.Grant(rules)
	}
	if ans.Always && g.Always != nil {
		g.Always(rules, ans.Everywhere)
	}
}

func (g *Gateway) levels() Levels {
	if g.Levels == nil {
		return Levels{}
	}
	return g.Levels()
}

func readOnly(t mcp.Tool) bool {
	return t.Annotations != nil && t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint
}
