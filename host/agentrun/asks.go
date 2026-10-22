package agentrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host/hostcore"
)

// askUser opens an ask and waits for the user's answer, or for ctx.
func (h *session) askUser(ctx context.Context, ask core.Ask) (AnswerReq, bool) {
	if ask.ID == "" {
		ask.ID = hostcore.NewID(6)
	}
	ask.Created = h.r.env.Now().UTC()
	ch := make(chan AnswerReq, 1)
	h.mu.Lock()
	h.waiting[ask.ID] = ch
	h.mu.Unlock()
	if err := h.r.st.Emit(core.AskOpened{Session: h.id, Ask: ask}); err != nil {
		return AnswerReq{}, false
	}
	hostcore.Restatus(h.r.st, h.id)
	defer hostcore.Restatus(h.r.st, h.id)
	select {
	case a, ok := <-ch:
		return a, ok
	case <-ctx.Done():
		h.mu.Lock()
		delete(h.waiting, ask.ID)
		h.mu.Unlock()
		_ = h.r.st.Emit(core.AskClosed{Session: h.id, ID: ask.ID, Decision: "gone"})
		return AnswerReq{}, false
	}
}

// answer is the user's answer to an ask.
func (h *session) answer(r AnswerReq) error {
	h.mu.Lock()
	ch, ok := h.waiting[r.Ask]
	delete(h.waiting, r.Ask)
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("nothing is asking %q", r.Ask)
	}
	ch <- r
	_ = h.r.st.Emit(core.AskClosed{Session: h.id, ID: r.Ask, Decision: r.Decision, Note: r.Note})
	return nil
}

// permission is layer 1 asking: the session's rules and the app's policy hooks answer first (layer 2), and the
// user only when neither does.
func (h *session) permission(sess agent.Session, p agent.Permission) {
	ask := core.Ask{ID: p.ID, Kind: core.AskPermission, Tool: p.Tool, Summary: p.Summary, Input: string(p.Input), Rule: p.Rule}
	if ask.Rule == "" {
		ask.Rule = RuleFor(p.Tool, p.Input)
	}
	if postsReview(p.Tool, p.Input) {
		_ = sess.Answer(h.ctx, p.ID, agent.Decision{Note: NoPosting})
		h.appendItem(core.Item{ID: hostcore.NewID(6), Kind: core.ItemSystem, Time: h.r.env.Now().UTC(), Text: p.Tool + " denied: agents never post to GitHub reviews"})
		return
	}
	if v, ok := h.r.policy(h.id, ask); ok {
		_ = sess.Answer(h.ctx, p.ID, agent.Decision{Allow: v.Decision == "allow", Note: v.Note})
		h.appendItem(core.Item{ID: hostcore.NewID(6), Kind: core.ItemSystem, Time: h.r.env.Now().UTC(),
			Text: fmt.Sprintf("%s %s by %s", p.Tool, map[bool]string{true: "allowed", false: "denied"}[v.Decision == "allow"], v.By)})
		return
	}
	a, ok := h.askUser(h.ctx, ask)
	if !ok {
		return
	}
	d := agent.Decision{Allow: a.Decision == "allow" || a.Decision == "always", Always: a.Decision == "always", Note: a.Note}
	if !d.Allow && d.Note == "" {
		d.Note = "Denied by the user in Oriel."
	} else if !d.Allow {
		d.Note = "Denied by the user in Oriel: " + d.Note
	}
	if d.Always {
		_ = h.r.addRule(h.id, ask.Rule)
	}
	_ = sess.Answer(h.ctx, p.ID, d)
}

func (h *session) question(sess agent.Session, q agent.Question) {
	a, ok := h.askUser(h.ctx, core.Ask{ID: q.ID, Kind: core.AskQuestion, Summary: questionSummary(q.Questions), Questions: q.Questions})
	if !ok {
		return
	}
	_ = sess.Answer(h.ctx, q.ID, agent.Decision{Allow: true, Answers: a.Answers, Note: a.Note})
}

func questionSummary(qs []core.Question) string {
	if len(qs) == 0 {
		return "The agent has a question"
	}
	return qs[0].Question
}

// approve is layer 3 asking: the sandbox holds a connection or a gateway call while the user decides.
func (h *session) approve(ctx context.Context, ask core.Ask) (hostcore.Approval, error) {
	if v, ok := h.r.policy(h.id, ask); ok {
		return hostcore.Approval{Allow: v.Decision == "allow", By: v.By, Note: v.Note}, nil
	}
	a, ok := h.askUser(ctx, ask)
	if !ok {
		return hostcore.Approval{}, ctx.Err()
	}
	if a.Decision == "always" && a.Scope == "" {
		_ = h.r.addRule(h.id, ask.Rule)
	}
	return hostcore.Approval{Allow: a.Decision == "allow" || a.Decision == "always", Scope: a.Scope, By: "the user", Note: a.Note}, nil
}

// RuleFor is the rule "Always" makes of a tool call: the command's first two words for a shell, the tool for
// the rest.
func RuleFor(tool string, input json.RawMessage) string {
	a := hostcore.ParseArgs(input)
	if cmd := a.Str("command"); cmd != "" {
		f := strings.Fields(cmd)
		if len(f) > 2 {
			f = f[:2]
		}
		return tool + "(" + strings.Join(f, " ") + ":*)"
	}
	return tool
}

// matchRule reports whether rule lets the ask through.
func matchRule(rule string, ask core.Ask) bool {
	if ask.Kind == core.AskNetwork {
		pat, ok := strings.CutPrefix(rule, "net(")
		if !ok {
			return false
		}
		return hostMatch(strings.TrimSuffix(pat, ")"), ask.Host)
	}
	tool, arg, hasArg := strings.Cut(rule, "(")
	if tool != ask.Tool {
		return false
	}
	if !hasArg {
		return true
	}
	arg = strings.TrimSuffix(arg, ")")
	cmd := hostcore.ParseArgs(json.RawMessage(ask.Input)).Str("command")
	if prefix, ok := strings.CutSuffix(arg, ":*"); ok {
		return cmd == prefix || strings.HasPrefix(cmd, prefix+" ")
	}
	return cmd == arg
}

func hostMatch(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		h, _, _ := strings.Cut(host, ":")
		p, port, hasPort := strings.Cut(rest, ":")
		if hasPort && !strings.HasSuffix(host, ":"+port) {
			return false
		}
		return h == p || strings.HasSuffix(h, "."+p)
	}
	return false
}

func (x *Runner) addRule(id core.SessionID, rule string) error {
	if rule == "" {
		return nil
	}
	return x.st.Emit(core.RuleAdded{Session: id, Rule: rule})
}

// policy is layer 2: the session's rules, then the app's hooks. ok is false when neither answers.
func (x *Runner) policy(id core.SessionID, ask core.Ask) (Verdict, bool) {
	se, err := x.st.Session(id)
	if err != nil {
		return Verdict{}, false
	}
	for _, r := range se.Rules {
		if matchRule(r, ask) {
			return Verdict{Decision: "allow", By: "rule " + r}, true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if v, ok := x.decide(ctx, Decide{Session: id, Ask: ask}); ok {
		if v.By == "" {
			v.By = "policy"
		}
		return v, true
	}
	return Verdict{}, false
}

// NoPosting is why an agent's attempt to post to a pull request's review or conversation is refused.
const NoPosting = "Oriel: agents never post to GitHub reviews. Answer the thread with mcp__oriel__reply: on a GitHub " +
	"thread your reply is a draft, and the user forwards it."

// postsReview says a tool call would post to a GitHub review: a review tool on any MCP server, or a gh command.
func postsReview(tool string, input json.RawMessage) bool {
	if gh.ReviewTool(tool) {
		return true
	}
	var in struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &in)
	return in.Command != "" && gh.PostsReview(in.Command)
}
