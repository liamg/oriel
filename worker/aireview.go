package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// AIReviewPack is the prompt pack an AI review's reviewer runs on.
const AIReviewPack = "ai-review"

// AIReviewTimeout bounds one AI review.
var AIReviewTimeout = 20 * time.Minute

// aiReviewDiffMax is how much of the change goes into the reviewer's prompt; it reads the files for the rest.
const aiReviewDiffMax = 120_000

// AIReview reviews a session's change with an agent other than the session's own (agents.md, Guardrails): a
// different provider from the author's by default, else the author's provider on another model. The reviewer is a
// short-lived session of the provider on the ai-review pack, which reads but changes nothing, and reports each finding
// with its finding tool. It runs in the background; its findings are session events as they come.
func (w *Worker) AIReview(ctx context.Context, r AIReviewReq) (_ string, err error) {
	defer w.recover("AIReview", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return "", err
	}
	if last, ok := se.LastAIReview(); ok && last.Running {
		return "", errors.New("an AI review of this session is running already")
	}
	p, model, err := w.reviewer(ctx, se, r)
	if err != nil {
		return "", err
	}
	rv := core.AIReview{ID: newID(4), Provider: p.ID(), Model: model, Author: authorLabel(w, ctx, se), Started: w.opts.Now().UTC(), Running: true}
	rv.Cross = rv.Provider != se.Agent.Provider || model != w.authorModel(ctx, se)
	last, _ := se.LastAIReview()
	var change strings.Builder
	total := 0
	for _, pr := range se.Projects {
		if pr.Dir == "" || !gitx.IsRepo(pr.Dir) {
			continue
		}
		repo := gitx.Repo{Dir: pr.Dir}
		rp := core.ReviewedProject{Project: pr.Name}
		if !r.Full {
			for _, lp := range last.Projects {
				if lp.Project == pr.Name && lp.Tree != "" {
					rp.Since, rp.From = lp.Head, lp.Tree
				}
			}
		}
		if rp.From == "" {
			base := pr.Base
			if pr.Detached {
				base = "" // a review's or a tour's start, not a branch it left
			}
			rp.Since = repo.MergeBase(ctx, base)
			rp.From = rp.Since
		}
		if rp.From == "" {
			continue
		}
		var err error
		if rp.Tree, err = repo.Snapshot(ctx); err != nil {
			return "", fmt.Errorf("%s: %w", pr.Name, err)
		}
		if head, err := repo.Head(ctx); err == nil {
			rp.Head = head[:min(7, len(head))]
		}
		patch, err := repo.Diff(ctx, rp.From, rp.Tree)
		if err != nil {
			return "", fmt.Errorf("%s: %w", pr.Name, err)
		}
		rv.Projects = append(rv.Projects, rp)
		if strings.TrimSpace(patch) == "" {
			continue
		}
		total += len(patch)
		if total > aiReviewDiffMax {
			patch = patch[:max(0, len(patch)-(total-aiReviewDiffMax))] + "\n… (the change goes on: read the files for the rest)\n"
		}
		what := "The change since " + rp.Since
		if rp.From != rp.Since {
			what = "What changed since the last review (at " + rp.Since + ")"
		}
		fmt.Fprintf(&change, "## %s\n\nDirectory: %s. %s, uncommitted work included:\n\n```diff\n%s\n```\n\n",
			pr.Name, pr.Dir, what, strings.TrimRight(patch, "\n"))
	}
	if change.Len() == 0 {
		if len(rv.Projects) > 0 && !r.Full && last.ID != "" {
			return "", fmt.Errorf("nothing has changed since the last AI review (%s)", rv.Since())
		}
		return "", errors.New("there's no change to review")
	}
	pack, ok := agent.Pack{}, false
	if r.Pack != "" {
		pack, ok = agent.Pack{Name: AIReviewPack, Skill: AIReviewPack, Text: r.Pack, Tools: []string{"finding", "projects"}}, true
	} else if w.opts.Packs != nil {
		pack, ok = w.pack(AIReviewPack)
	}
	if !ok {
		return "", errors.New("there is no ai-review prompt pack")
	}
	if err := w.emit(core.AIReviewChanged{Session: se.ID, Review: rv}); err != nil {
		return "", err
	}
	run := &aiReviewRun{w: w, session: se.ID, review: rv}
	prompt := reviewPrompt(se, rv, change.String())
	go run.run(p, agent.StartSpec{Cwd: agentDir(se), Model: model, Pack: pack, Prompt: prompt, Tools: run.tools(), Policy: agent.PolicyAsk,
		Protect: w.protected(se), Exec: w.opts.Exec, StateDir: filepath.Join(w.sessionDir(se.ID), "ai-review", rv.ID),
		Env: []string{"ORIEL_SESSION=" + string(se.ID)}})
	return rv.ID, nil
}

// reviewer is the provider and model that review a session's change: the request's, else the first installed
// provider other than the author's at its default model, else the author's provider on a model other than its own.
func (w *Worker) reviewer(ctx context.Context, se core.SessionState, r AIReviewReq) (agent.Provider, string, error) {
	if r.Provider != "" {
		p, ok := w.opts.Providers.Get(r.Provider)
		if !ok {
			return nil, "", fmt.Errorf("no agent provider %q", r.Provider)
		}
		return p, cmp.Or(r.Model, defaultModel(ctx, p)), nil
	}
	author := se.Agent.Provider
	var mine agent.Provider
	for _, p := range w.opts.Providers.List() {
		if !w.installed(ctx, p) {
			continue
		}
		if p.ID() != author {
			return p, cmp.Or(r.Model, defaultModel(ctx, p)), nil
		}
		mine = p
	}
	if mine == nil {
		return nil, "", errors.New("no agent provider is installed and signed in to review with")
	}
	if r.Model != "" {
		return mine, r.Model, nil
	}
	own := w.authorModel(ctx, se)
	ms, _ := mine.Models(ctx)
	slices.SortStableFunc(ms, func(a, b agent.Model) int {
		switch {
		case a.Default && !b.Default:
			return -1
		case b.Default && !a.Default:
			return 1
		}
		return 0
	})
	for _, m := range ms {
		if m.ID != own {
			return mine, m.ID, nil
		}
	}
	return mine, own, nil
}

// installed reports whether a provider can start a session here now.
func (w *Worker) installed(ctx context.Context, p agent.Provider) bool {
	if w.signedOut(p.ID()) {
		return false
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	in, err := p.Detect(dctx)
	return err == nil && in.LoggedIn
}

func defaultModel(ctx context.Context, p agent.Provider) string {
	ms, err := p.Models(ctx)
	if err != nil || len(ms) == 0 {
		return ""
	}
	for _, m := range ms {
		if m.Default {
			return m.ID
		}
	}
	return ms[0].ID
}

// authorModel is the model the session's agent runs on: its own, else its provider's default.
func (w *Worker) authorModel(ctx context.Context, se core.SessionState) string {
	if se.Agent.Model != "" {
		return se.Agent.Model
	}
	if p, ok := w.opts.Providers.Get(se.Agent.Provider); ok {
		return defaultModel(ctx, p)
	}
	return ""
}

func authorLabel(w *Worker, ctx context.Context, se core.SessionState) string {
	if se.Agent.Provider == "" {
		return ""
	}
	if m := w.authorModel(ctx, se); m != "" {
		return se.Agent.Provider + " · " + m
	}
	return se.Agent.Provider
}

// reviewPrompt is what the reviewer is asked: who wrote the change, the change, and what earlier reviews found.
func reviewPrompt(se core.SessionState, rv core.AIReview, change string) string {
	var b strings.Builder
	switch {
	case se.Kind == core.KindReview:
		b.WriteString("Review this pull request")
		if se.PR != "" {
			b.WriteString(" (" + se.PR + ")")
		}
		b.WriteString(".\n\n")
	case rv.Author != "":
		fmt.Fprintf(&b, "Review this change. %s wrote it; you're the second opinion.\n\n", rv.Author)
	default:
		b.WriteString("Review this change.\n\n")
	}
	if se.Prompt != "" {
		fmt.Fprintf(&b, "What it was asked to do:\n\n> %s\n\n", strings.ReplaceAll(strings.TrimSpace(se.Prompt), "\n", "\n> "))
	}
	b.WriteString(change)
	if len(se.Findings) > 0 {
		b.WriteString("## Earlier findings\n\nDon't report these again.\n\n")
		for _, f := range se.SortedFindings() {
			state := "open"
			switch {
			case f.Dismissed:
				state = "dismissed by the user"
			case f.Thread != "":
				state = "handed to the author"
			}
			fmt.Fprintf(&b, "- [%s] %s", f.Severity, f.Title)
			if w := f.Where(); w != "" {
				b.WriteString(" (" + w + ")")
			}
			b.WriteString(": " + state + "\n")
		}
	}
	return b.String()
}

// aiReviewRun is one AI review as it runs.
type aiReviewRun struct {
	w       *Worker
	session core.SessionID
	review  core.AIReview

	mu    sync.Mutex
	found int
}

func (r *aiReviewRun) tools() []agent.Tool {
	sev := `{"type":"string","enum":["critical","high","medium","low","nit"]}`
	h := &host{w: r.w, id: r.session}
	return []agent.Tool{
		{Name: "finding", Description: "Report one finding of the review: severity, a one-word category (correctness, tests, api, security, performance, style, docs), a title of a few words, the file (relative to its project) and line in the new code, a body of one to three sentences, project when the session has several, and repo when it's about a repository outside the session.",
			Schema: schema(`"severity":`+sev+`,"category":`+str+`,"title":`+str+`,"body":`+str+`,"file":`+str+`,"line":`+num+`,"project":`+str+`,"repo":`+str, "severity", "title"),
			Call:   r.toolFinding},
		{Name: "projects", Description: "List the session's projects: name, directory, branch, and whether it has uncommitted changes.", ReadOnly: true,
			Schema: schema(""), Call: h.toolProjects},
	}
}

func (r *aiReviewRun) toolFinding(_ context.Context, raw json.RawMessage) (string, error) {
	var in rawFinding
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	f, ok := in.finding()
	if !ok {
		return "", errors.New("a finding needs a title")
	}
	if !r.record([]core.Finding{f}) {
		return "already reported", nil
	}
	return "recorded", nil
}

// record keeps findings the session hasn't got already; it reports whether it kept any.
func (r *aiReviewRun) record(fs []core.Finding) bool {
	se, err := r.w.session(r.session)
	if err != nil {
		return false
	}
	var keep []core.Finding
	for _, f := range fs {
		if slices.ContainsFunc(append(slices.Clone(se.Findings), keep...), func(o core.Finding) bool { return sameFinding(o, f) }) {
			continue
		}
		f.ID, f.Review = newID(4), r.review.ID
		if f.Project == "" && len(se.Projects) > 0 {
			if p, ok := se.Primary(); ok {
				f.Project = p.Name
			}
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		return false
	}
	if r.w.emit(core.FindingsRecorded{Session: r.session, Findings: keep}) != nil {
		return false
	}
	r.mu.Lock()
	r.found += len(keep)
	r.mu.Unlock()
	return true
}

func sameFinding(a, b core.Finding) bool {
	return a.File == b.File && strings.EqualFold(strings.TrimSpace(a.Title), strings.TrimSpace(b.Title)) && (a.Project == b.Project || b.Project == "")
}

// run starts the reviewer, answers it until its turn ends, and records how the review went.
func (r *aiReviewRun) run(p agent.Provider, spec agent.StartSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), AIReviewTimeout)
	defer cancel()
	var said []string
	errText := ""
	sess, err := p.Start(ctx, spec)
	if err != nil {
		errText = err.Error()
	} else {
		said, errText = r.converse(ctx, sess)
		_ = sess.Close()
	}
	r.mu.Lock()
	found := r.found
	r.mu.Unlock()
	if found == 0 {
		for _, text := range slices.Backward(said) {
			if fs := ParseFindings(text); len(fs) > 0 {
				r.record(fs)
				break
			}
		}
	}
	rv := r.review
	rv.Running, rv.Ended, rv.Error = false, r.w.opts.Now().UTC(), errText
	if len(said) > 0 {
		rv.Verdict = verdict(said[len(said)-1])
	}
	_ = r.w.emit(core.AIReviewChanged{Session: r.session, Review: rv})
}

// converse answers the reviewer until its turn ends: every permission is refused, since it only reads. It returns
// what the reviewer said, and why the review failed if it did.
func (r *aiReviewRun) converse(ctx context.Context, sess agent.Session) ([]string, string) {
	var said []string
	failed := ""
	for {
		select {
		case <-ctx.Done():
			return said, "the reviewer took too long"
		case <-r.w.closed:
			return said, "Oriel stopped during the review"
		case ev, ok := <-sess.Events():
			if !ok {
				return said, "the reviewer exited before it finished"
			}
			switch ev := ev.(type) {
			case agent.Message:
				if !ev.Partial && ev.Agent == "" && (ev.Role == "" || ev.Role == core.ItemAssistant) && strings.TrimSpace(ev.Text) != "" {
					said = append(said, ev.Text)
				}
			case agent.Permission:
				_ = sess.Answer(ctx, ev.ID, agent.Decision{Note: "The AI review only reads: read the files instead, and change nothing."})
			case agent.Question:
				_ = sess.Answer(ctx, ev.ID, agent.Decision{Note: "Nobody answers questions during an AI review: decide for yourself, and say so in the finding."})
			case agent.Failure:
				failed = ev.Err
			case agent.AuthExpired:
				return said, ev.Error()
			case agent.Exited:
				return said, "the reviewer exited before it finished"
			case agent.TurnEnd:
				return said, failed
			}
		}
	}
}

// verdict is the last line the reviewer said, outside any code block.
func verdict(text string) string {
	text = fenced.ReplaceAllString(text, "")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, l := range slices.Backward(lines) {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return ""
}

// SetFindings records findings as the user changed them: dismissed, or back.
func (w *Worker) SetFindings(r core.FindingsRecorded) (err error) {
	defer w.recover("SetFindings", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	for i, f := range r.Findings {
		old, ok := se.Finding(f.ID)
		if !ok {
			return fmt.Errorf("no finding %s", f.ID)
		}
		old.Dismissed = f.Dismissed
		r.Findings[i] = old
	}
	return w.emit(r)
}

// rawFinding is a finding as a reviewer gives it, through the tool or in a JSON block, read leniently.
type rawFinding struct {
	Severity    string          `json:"severity"`
	Category    string          `json:"category"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	Description string          `json:"description"`
	File        string          `json:"file"`
	Path        string          `json:"path"`
	Line        json.RawMessage `json:"line"`
	Project     string          `json:"project"`
	Repo        string          `json:"repo"`
}

var severityWords = map[string]string{
	"critical": "critical", "blocker": "critical", "severe": "critical",
	"high": "high", "major": "high", "error": "high",
	"medium": "medium", "med": "medium", "moderate": "medium",
	"low": "low", "minor": "low", "warning": "low",
	"nit": "nit", "nitpick": "nit", "info": "nit", "trivial": "nit",
}

var fileLine = regexp.MustCompile(`^(.+?):(\d+)(?:[-:]\d+)?$`)

func (in rawFinding) finding() (core.Finding, bool) {
	f := core.Finding{Category: strings.ToLower(strings.TrimSpace(in.Category)), Title: strings.TrimSpace(in.Title),
		Body: strings.TrimSpace(in.Body), File: strings.TrimSpace(in.File), Project: strings.TrimSpace(in.Project), Repo: strings.TrimSpace(in.Repo)}
	if f.Body == "" {
		f.Body = strings.TrimSpace(in.Description)
	}
	if f.Title == "" {
		f.Title, _, _ = strings.Cut(f.Body, "\n")
	}
	if f.Title == "" {
		return f, false
	}
	f.Severity = severityWords[strings.ToLower(strings.TrimSpace(in.Severity))]
	if f.Severity == "" {
		f.Severity = "low"
	}
	if f.File == "" {
		f.File = strings.TrimSpace(in.Path)
	}
	var n json.Number
	var s string
	switch {
	case json.Unmarshal(in.Line, &n) == nil:
		v, _ := n.Int64()
		f.Line = int(v)
	case json.Unmarshal(in.Line, &s) == nil:
		s, _, _ = strings.Cut(s, "-")
		f.Line, _ = strconv.Atoi(strings.TrimSpace(s))
	}
	if m := fileLine.FindStringSubmatch(f.File); m != nil {
		f.File = m[1]
		if f.Line == 0 {
			f.Line, _ = strconv.Atoi(m[2])
		}
	}
	f.File = strings.TrimPrefix(filepath.ToSlash(f.File), "./")
	return f, true
}

var (
	fenced    = regexp.MustCompile("(?s)```[a-zA-Z]*\\s*\\n(.*?)```")
	jsonStart = regexp.MustCompile(`(?m)^\s*[\[{]`)
)

// ParseFindings reads the findings a reviewer wrote as JSON in its answer: a fenced block or a bare one, either
// {"findings": [...]} or the list itself.
func ParseFindings(text string) []core.Finding {
	var blocks []string
	for _, m := range fenced.FindAllStringSubmatch(text, -1) {
		blocks = append(blocks, m[1])
	}
	if loc := jsonStart.FindStringIndex(text); loc != nil {
		blocks = append(blocks, text[loc[0]:])
	}
	for _, b := range blocks {
		dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(b)))
		var v any
		if dec.Decode(&v) != nil {
			continue
		}
		raw, _ := json.Marshal(v)
		var list []rawFinding
		if obj, ok := v.(map[string]any); ok {
			inner, _ := json.Marshal(obj["findings"])
			if json.Unmarshal(inner, &list) != nil {
				continue
			}
		} else if json.Unmarshal(raw, &list) != nil {
			continue
		}
		var out []core.Finding
		for _, in := range list {
			if f, ok := in.finding(); ok {
				out = append(out, f)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}
