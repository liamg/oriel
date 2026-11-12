package agentrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/guard"
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
	a, ok := h.ask(h.ctx, q.ID, q.Thread, q.Questions)
	if !ok {
		return
	}
	_ = sess.Answer(h.ctx, q.ID, agent.Decision{Allow: true, Answers: a.Answers, Note: a.Note})
}

// ask asks the user questions in a thread (questionThread's) and waits for the answer. A thread opened for the
// question keeps the answer and is resolved once it's given.
func (h *session) ask(ctx context.Context, id string, named core.ThreadID, qs []core.Question) (AnswerReq, bool) {
	tid, opened, err := h.questionThread(named, qs)
	if err != nil {
		return AnswerReq{}, false
	}
	a, ok := h.askUser(ctx, core.Ask{ID: id, Kind: core.AskQuestion, Summary: questionSummary(qs), Questions: qs, Thread: tid})
	if ok && opened {
		_, _ = h.r.mutateThread(h.id, tid, func(t *core.Thread) error {
			if body := answerText(qs, a); body != "" {
				t.Comments = append(t.Comments, core.Comment{ID: hostcore.NewID(6), Author: core.AuthorYou, Name: hostcore.WhoAmI(ctx), Body: body,
					Time: h.r.env.Now().UTC(), Delivered: true})
			}
			t.Status, t.Busy, t.Active = core.ThreadResolved, false, false
			return nil
		})
		hostcore.Restatus(h.r.st, h.id)
	}
	return a, ok
}

// questionThread is the thread a question is asked in: the one named if it's there; else the one the agent is on (an
// active thread, the latest touched), else one it was handed and is busy with, else the session's request (its first
// open general thread). With none, it opens a general thread holding the question, and says so.
func (h *session) questionThread(named core.ThreadID, qs []core.Question) (core.ThreadID, bool, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return "", false, err
	}
	if t, ok := se.Thread(named); ok && named != "" {
		return t.ID, false, nil
	}
	mine := func(t core.Thread) bool { return t.Open() && !t.Btw && !t.Outgoing }
	for _, want := range []func(core.Thread) bool{
		func(t core.Thread) bool { return t.Active },
		func(t core.Thread) bool { return t.Busy },
	} {
		var best *core.Thread
		for i, t := range se.Threads {
			if mine(t) && want(t) && (best == nil || t.Updated.After(best.Updated)) {
				best = &se.Threads[i]
			}
		}
		if best != nil {
			return best.ID, false, nil
		}
	}
	for _, t := range se.Threads {
		if mine(t) && t.Anchor == nil && len(t.Comments) > 0 {
			return t.ID, false, nil
		}
	}
	now := h.r.env.Now().UTC()
	t := core.Thread{ID: core.ThreadID(hostcore.NewID(8)), Title: questionSummary(qs), Status: core.ThreadOpen, Created: now, Updated: now,
		Comments: []core.Comment{{ID: hostcore.NewID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: questionsText(qs), Time: now,
			Model: modelLabel(se.Agent, h.ranModel())}}}
	if err := h.r.st.Emit(core.ThreadUpdated{Session: h.id, Thread: t}); err != nil {
		return "", false, err
	}
	return t.ID, true, nil
}

// questionsText is questions as a comment: each on its own line, with its options.
func questionsText(qs []core.Question) string {
	var lines []string
	for _, q := range qs {
		line := q.Question
		if len(q.Options) > 0 {
			line += " (" + strings.Join(q.Options, " / ") + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}

// answerText is the user's answers to questions, as a comment or the ask tool's result: each question and its answer.
func answerText(qs []core.Question, a AnswerReq) string {
	var lines []string
	for _, q := range qs {
		if ans, ok := a.Answers[q.Question]; ok {
			lines = append(lines, q.Question+" → "+ans)
		}
	}
	if n := strings.TrimSpace(a.Note); n != "" {
		lines = append(lines, n)
	}
	return strings.Join(lines, "\n\n")
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
const NoPosting = guard.NoPosting

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
