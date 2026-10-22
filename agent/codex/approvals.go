package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/guard"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
)

// request is what Oriel reads of the app-server's requests.
type request struct {
	ThreadID  string   `json:"threadId"`
	TurnID    string   `json:"turnId"`
	ItemID    string   `json:"itemId"`
	Command   string   `json:"command"`
	Cwd       string   `json:"cwd"`
	Reason    string   `json:"reason"`
	GrantRoot string   `json:"grantRoot"`
	Amendment []string `json:"proposedExecpolicyAmendment"`
	Questions []struct {
		ID       string `json:"id"`
		Header   string `json:"header"`
		Question string `json:"question"`
		Options  []struct {
			Label string `json:"label"`
		} `json:"options"`
	} `json:"questions"`
	ServerName string `json:"serverName"`
	Message    string `json:"message"`
}

// ours says a thread's requests are the session's to answer: its own, or a subagent's it spawned.
func (s *session) ours(thread string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return thread == s.id || s.children[thread]
}

func (s *session) onRequest(r *jsonrpc.Request) {
	var p request
	json.Unmarshal(r.Params, &p)
	main := s.ours(p.ThreadID)
	switch r.Method {
	case "item/commandExecution/requestApproval":
		if !main {
			r.Reply(map[string]any{"decision": "decline"}, nil)
			return
		}
		s.commandApproval(r, p)
	case "item/fileChange/requestApproval":
		if !main {
			r.Reply(map[string]any{"decision": "decline"}, nil)
			return
		}
		s.fileApproval(r, p)
	case "item/tool/requestUserInput":
		if !main {
			r.Reply(map[string]any{"answers": map[string]any{}}, nil)
			return
		}
		s.userInput(r, p)
	case "mcpServer/elicitation/request":
		if p.ServerName == "oriel" {
			r.Reply(map[string]any{"action": "accept", "content": map[string]any{}}, nil)
			return
		}
		if !main {
			r.Reply(map[string]any{"action": "decline"}, nil)
			return
		}
		s.elicitation(r, p)
	default:
		r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "oriel doesn't answer " + r.Method})
	}
}

// refuse declines a request for the guard rails, and tells the turn why.
func (s *session) refuse(r *jsonrpc.Request, p request, decline map[string]any, reason string) {
	r.Reply(decline, nil)
	go s.tell(p.ThreadID, p.TurnID, "Denied by Oriel: "+reason)
}

// approval answers an approval request with Decision d: accepted, for the session with Always, else declined, with
// the denial's note told to the turn.
func (s *session) approval(r *jsonrpc.Request, p request, d agent.Decision) error {
	decision := "decline"
	switch {
	case d.Allow && d.Always:
		decision = "acceptForSession"
	case d.Allow:
		decision = "accept"
	}
	if err := r.Reply(map[string]any{"decision": decision}, nil); err != nil {
		return err
	}
	if !d.Allow && strings.TrimSpace(d.Note) != "" {
		s.tell(p.ThreadID, p.TurnID, "Denied by the user: "+strings.TrimSpace(d.Note))
	}
	return nil
}

func (s *session) commandApproval(r *jsonrpc.Request, p request) {
	in := map[string]any{"command": p.Command, "cwd": p.Cwd}
	if p.Reason != "" {
		in["reason"] = p.Reason
	}
	input := jsonOf(in)
	if d := guard.Check(s.spec, "Bash", input); d.Deny {
		s.refuse(r, p, map[string]any{"decision": "decline"}, d.Reason)
		return
	}
	perm := agent.Permission{Tool: "Bash", Summary: p.Command, Input: input, Rule: commandRule(p.Amendment)}
	perm.ID = s.ask(func(d agent.Decision) error { return s.approval(r, p, d) })
	s.emit(perm)
}

// commandRule is the rule "Always" makes of a command, in Oriel's form: Bash(go test:*) from the prefix codex
// proposes allowing; "" leaves it to the session host.
func commandRule(amendment []string) string {
	if len(amendment) == 0 {
		return ""
	}
	return "Bash(" + strings.Join(amendment, " ") + ":*)"
}

func (s *session) fileApproval(r *jsonrpc.Request, p request) {
	s.mu.Lock()
	var paths []string
	for _, c := range s.changes[p.ItemID] {
		paths = append(paths, c.Path)
	}
	s.mu.Unlock()
	decline := map[string]any{"decision": "decline"}
	for _, f := range append(paths, p.GrantRoot) {
		if d := guard.Protected(s.spec, f); d.Deny {
			s.refuse(r, p, decline, d.Reason)
			return
		}
	}
	if s.spec.Policy == agent.PolicyLight && p.GrantRoot == "" {
		r.Reply(map[string]any{"decision": "accept"}, nil)
		return
	}
	perm := agent.Permission{Tool: "Edit", Summary: strings.Join(paths, ", ")}
	in := map[string]any{"files": paths}
	if len(paths) > 0 {
		in["file_path"] = paths[0]
	}
	if p.GrantRoot != "" {
		in["grantRoot"] = p.GrantRoot
		if perm.Summary == "" {
			perm.Summary = "write under " + p.GrantRoot
		}
	}
	if p.Reason != "" {
		in["reason"] = p.Reason
	}
	perm.Input = jsonOf(in)
	perm.ID = s.ask(func(d agent.Decision) error { return s.approval(r, p, d) })
	s.emit(perm)
}

func (s *session) userInput(r *jsonrpc.Request, p request) {
	q := agent.Question{}
	for _, pq := range p.Questions {
		cq := core.Question{Header: pq.Header, Question: pq.Question}
		for _, o := range pq.Options {
			cq.Options = append(cq.Options, o.Label)
		}
		q.Questions = append(q.Questions, cq)
	}
	qs := p.Questions
	q.ID = s.ask(func(d agent.Decision) error {
		answers := map[string]any{}
		for i, pq := range qs {
			for _, key := range []string{pq.Question, pq.ID, pq.Header} {
				if a, ok := d.Answers[key]; ok && key != "" {
					answers[pq.ID] = map[string]any{"answers": picks(a, q.Questions[i].Options)}
					break
				}
			}
		}
		if err := r.Reply(map[string]any{"answers": answers}, nil); err != nil {
			return err
		}
		if len(answers) == 0 {
			note := strings.TrimSpace(d.Note)
			if note == "" {
				note = "The user chose not to answer."
			}
			s.tell(p.ThreadID, p.TurnID, note)
		}
		return nil
	})
	s.emit(q)
}

// picks are an answer's choices: options picked together come joined with ", ", as Oriel's question card joins
// them; anything else is one answer.
func picks(a string, options []string) []string {
	parts := strings.Split(a, ", ")
	if len(parts) < 2 {
		return []string{a}
	}
	for _, p := range parts {
		if !slices.Contains(options, p) {
			return []string{a}
		}
	}
	return parts
}

// elicitation is an MCP server asking: approval for one of its tools, taken to be the call to it under way.
func (s *session) elicitation(r *jsonrpc.Request, p request) {
	tool, args := "mcp__"+p.ServerName, json.RawMessage(nil)
	s.mu.Lock()
	for _, c := range s.calls {
		if c.Server == p.ServerName {
			tool, args = "mcp__"+c.Server+"__"+str(c.Tool), c.Arguments
		}
	}
	s.mu.Unlock()
	if d := guard.Check(s.spec, tool, args); d.Deny {
		s.refuse(r, p, map[string]any{"action": "decline"}, d.Reason)
		return
	}
	perm := agent.Permission{Tool: tool, Summary: p.Message, Input: r.Params}
	perm.ID = s.ask(func(d agent.Decision) error {
		if d.Allow {
			return r.Reply(map[string]any{"action": "accept", "content": map[string]any{}}, nil)
		}
		if err := r.Reply(map[string]any{"action": "decline"}, nil); err != nil {
			return err
		}
		if note := strings.TrimSpace(d.Note); note != "" && p.TurnID != "" {
			s.tell(p.ThreadID, p.TurnID, "Denied by the user: "+note)
		}
		return nil
	})
	s.emit(perm)
}
