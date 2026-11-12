// Package aireview reviews a session's change with a short-lived reviewer agent.
package aireview

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// AIReviewPack is the prompt pack an AI review's reviewer runs on.
const AIReviewPack = "ai-review"

// AIReviewTimeout bounds one AI review.
var AIReviewTimeout = 20 * time.Minute

// aiReviewDiffMax is how much of the change goes into the reviewer's prompt; it reads the files for the rest.
const aiReviewDiffMax = 120_000

// AIReviewReq asks for a cross-model AI review of a session's change.
type AIReviewReq struct {
	Session  core.SessionID `json:"session"`
	Provider string         `json:"provider,omitempty"` // the reviewer's; "" for the session agent's
	Model    string         `json:"model,omitempty"`    // "" for the provider's default
	// Full reviews the whole change since the base; otherwise a session reviewed before has what changed since.
	Full bool `json:"full,omitempty"`
	// Pack is the reviewer's prompt, when it isn't the built-in ai-review pack: the user's own.
	Pack string `json:"pack,omitempty"`
}

// Reviews runs AI reviews.
type Reviews struct {
	env  hostcore.Env
	st   hostcore.State
	deps Deps
	runs sync.WaitGroup // reviews running
}

// Deps are what a review needs of the rest of the host.
type Deps struct {
	Providers *agent.Providers
	Exec      func() agent.Exec
	// Pack is a prompt pack by name, as the user has it.
	Pack func(name string) (agent.Pack, bool)
	// Protected are the checkouts an agent of the session must never edit.
	Protected func(se core.SessionState) []string
	// SignedOut says a provider's login here has stopped working.
	SignedOut func(provider string) bool
}

// New runs reviews of st's sessions.
func New(env hostcore.Env, st hostcore.State, deps Deps) *Reviews {
	return &Reviews{env: env, st: st, deps: deps}
}

// Wait waits for the reviews running to end, as they do once the host closes.
func (x *Reviews) Wait() { x.runs.Wait() }

// Settle ends the reviews a session had running when the last process ended.
func (x *Reviews) Settle(se core.SessionState) {
	for _, rv := range se.AIReviews {
		if rv.Running {
			rv.Running, rv.Ended, rv.Error = false, x.env.Now().UTC(), "Oriel ended during the review"
			_ = x.st.Emit(core.AIReviewChanged{Session: se.ID, Review: rv})
		}
	}
}

// AIReview reviews a session's change on the session agent's provider and model unless the request picks one. The
// reviewer is a short-lived session of the provider on the ai-review pack, which reads but changes nothing, and reports each finding
// with its finding tool. It runs in the background; its findings are session events as they come.
func (x *Reviews) AIReview(ctx context.Context, r AIReviewReq) (string, error) {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return "", err
	}
	if x.env.IsClosed() {
		return "", errors.New("the session host is closed")
	}
	if last, ok := se.LastAIReview(); ok && last.Running {
		return "", errors.New("an AI review of this session is running already")
	}
	p, model, err := x.reviewer(ctx, se, r)
	if err != nil {
		return "", err
	}
	rv := core.AIReview{ID: hostcore.NewID(4), Provider: p.ID(), Model: model, Author: x.authorLabel(ctx, se), Started: x.env.Now().UTC(), Running: true}
	last, _ := se.LastAIReview()
	change, err := changeOf(ctx, se, &rv, last, r.Full)
	if err != nil {
		return "", err
	}
	if change == "" {
		if len(rv.Projects) > 0 && !r.Full && last.ID != "" {
			return "", fmt.Errorf("nothing has changed since the last AI review (%s)", rv.Since())
		}
		return "", errors.New("there's no change to review")
	}
	pack, ok := agent.Pack{}, false
	if r.Pack != "" {
		pack, ok = agent.Pack{Name: AIReviewPack, Skill: AIReviewPack, Text: r.Pack, Tools: []string{"finding", "projects"}}, true
	} else {
		pack, ok = x.deps.Pack(AIReviewPack)
	}
	if !ok {
		return "", errors.New("there is no ai-review prompt pack")
	}
	if err := x.st.Emit(core.AIReviewChanged{Session: se.ID, Review: rv}); err != nil {
		return "", err
	}
	run := &aiReviewRun{x: x, session: se.ID, review: rv}
	spec := agent.StartSpec{Cwd: hostcore.AgentDir(se), Model: model, Pack: pack, Prompt: reviewPrompt(se, rv, change), Tools: run.tools(),
		Policy: agent.PolicyAsk, Protect: x.deps.Protected(se), Exec: x.deps.Exec(),
		StateDir: filepath.Join(x.env.SessionDir(se.ID), "ai-review", rv.ID), Env: x.env.AgentEnv(se.ID)}
	x.runs.Go(func() {
		var err error
		defer func() {
			if err != nil {
				run.end(nil, err.Error())
			}
		}()
		defer x.env.Recover("AI review", &err)
		run.run(p, spec)
	})
	return rv.ID, nil
}

// changeOf is the change the reviewer reads, as a diff per project: since the last review's tree unless full, else
// since the base. Each project with a change goes on rv.
func changeOf(ctx context.Context, se core.SessionState, rv *core.AIReview, last core.AIReview, full bool) (string, error) {
	var change strings.Builder
	total := 0
	for _, pr := range se.Projects {
		if pr.Dir == "" || !gitx.IsRepo(pr.Dir) {
			continue
		}
		repo := gitx.Repo{Dir: pr.Dir}
		rp := core.ReviewedProject{Project: pr.Name}
		if !full {
			for _, lp := range last.Projects {
				if lp.Project == pr.Name && lp.Tree != "" {
					rp.Since, rp.From = lp.Head, lp.Tree
				}
			}
		}
		if rp.From == "" {
			base := pr.Base
			if pr.Detached {
				base = "" // a review's start, not a branch it left
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
	return change.String(), nil
}

// reviewer is the provider and model that review a session's change: the request's, else the session agent's.
// A session with no agent is reviewed by the first signed-in provider at its default model.
func (x *Reviews) reviewer(ctx context.Context, se core.SessionState, r AIReviewReq) (agent.Provider, string, error) {
	if r.Provider != "" {
		p, ok := x.deps.Providers.Get(r.Provider)
		if !ok {
			return nil, "", fmt.Errorf("no agent provider %q", r.Provider)
		}
		return p, cmp.Or(r.Model, hostcore.DefaultModel(ctx, p)), nil
	}
	if p, ok := x.deps.Providers.Get(se.Agent.Provider); ok {
		return p, cmp.Or(r.Model, x.authorModel(ctx, se)), nil
	}
	for _, p := range x.deps.Providers.List() {
		if !x.deps.SignedOut(p.ID()) && hostcore.SignedIn(ctx, p) {
			return p, cmp.Or(r.Model, hostcore.DefaultModel(ctx, p)), nil
		}
	}
	return nil, "", errors.New("no agent provider is installed and signed in to review with")
}

// authorModel is the model the session's agent runs on: its own, else its provider's default.
func (x *Reviews) authorModel(ctx context.Context, se core.SessionState) string {
	if se.Agent.Model != "" {
		return se.Agent.Model
	}
	if p, ok := x.deps.Providers.Get(se.Agent.Provider); ok {
		return hostcore.DefaultModel(ctx, p)
	}
	return ""
}

func (x *Reviews) authorLabel(ctx context.Context, se core.SessionState) string {
	if se.Agent.Provider == "" {
		return ""
	}
	if m := x.authorModel(ctx, se); m != "" {
		return se.Agent.Provider + " · " + m
	}
	return se.Agent.Provider
}

// reviewPrompt is what the reviewer is asked: who wrote the change, the change, and what earlier reviews found.
func reviewPrompt(se core.SessionState, rv core.AIReview, change string) string {
	var b strings.Builder
	intro, reviewing := reviewIntro(se)
	switch {
	case reviewing:
		b.WriteString(intro)
	case rv.Author != "":
		fmt.Fprintf(&b, "Review this change. %s wrote it.\n\n", rv.Author)
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
