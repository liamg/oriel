package ship

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// GitHubHost is GitHub as a code host, built in on the user's own gh, run by the session host with the checkout's
// env: one GraphQL search per list for the Reviews page, the REST calls for a PR in full, GitHub's pull/N/head to check
// one out, and a project's PR raised, read and merged.
type GitHubHost struct {
	kit.Disposer
	h plugin.Host
}

var (
	githubPRPath = regexp.MustCompile(`^/[^/]+/[^/]+/pull/\d+`)
	githubPath   = regexp.MustCompile(`^/?([^/]+/[^/]+?)(?:\.git)?/?$`)
)

func (x *GitHubHost) Manifest() plugin.Manifest { return plugin.Manifest{ID: "github-host"} }

func (x *GitHubHost) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.Add(h.Contribute(registry.CodeHost{Name: "github", Title: "GitHub", Icon: "pullrequest", Order: 10,
		Sites: []registry.Site{{Host: "github.com", Secret: "GITHUB_TOKEN", Aliases: []string{"ssh.github.com"}}}, Path: githubPath, Takes: githubPRPath,
		Setup: registry.SiteSetup{Secret: "GITHUB_TOKEN", TokenURL: "https://{host}/settings/tokens/new?scopes=repo,read:org&description=Oriel",
			TokenHelp: "Oriel uses gh's login when gh is installed (gh auth login --hostname {host}). Otherwise, a classic token with the repo and read:org scopes.",
			Probe:     x.probe},
		Tokens: registry.TokenSources{Env: []string{"GH_TOKEN", "GITHUB_TOKEN"}, HostedEnv: []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"},
			Command: []string{"gh", "auth", "token", "--hostname", "{host}"}, Optional: true},
		Requested: x.requested, Lookup: x.lookup, Checkout: githubCheckout, Submit: x.submit, React: x.react, Unreact: x.unreact,
		ForBranch: x.forBranch, Create: x.create, Edit: x.edit, Ready: x.ready, RequestReviewers: x.reviewers, Checks: x.checks,
		MergeMethods: x.mergeMethods, Blockers: x.blockers, Merge: x.merge, Source: "builtin:github"}))
	return nil
}

func (x *GitHubHost) host() (host.Access, error) { return kit.SessionHost(x.h) }

// probe says hostname is a GitHub Enterprise server: its /api/v3/meta answers GitHub's JSON with no token.
func (x *GitHubHost) probe(ctx context.Context, hostname string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "https://"+hostname+"/api/v3/meta", nil)
	if err != nil {
		return false, err
	}
	resp, err := x.h.Net().Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var meta struct {
		Installed *bool `json:"verifiable_password_authentication"`
	}
	return resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&meta) == nil && meta.Installed != nil, nil
}

func (x *GitHubHost) requested(ctx context.Context, s registry.Site) ([]registry.ReviewRequest, error) {
	w, err := x.host()
	if err != nil {
		return nil, err
	}
	queue, err := w.ReviewQueue(ctx, host.ReviewQueueReq{Host: s.Host, Token: x.token(s)})
	if err != nil {
		return nil, err
	}
	out := make([]registry.ReviewRequest, 0, len(queue))
	for _, a := range queue {
		r := registry.ReviewRequest{URL: a.URL, Repo: a.Repo, Number: a.Number, Title: a.Title, Author: a.Author, Draft: a.Draft, Asked: a.Asked,
			Since: a.Since, Adds: a.Additions, Dels: a.Deletions, Files: a.Files, CI: ciWord(a.CI), Base: a.Base, Head: a.Head,
			Opened: a.Created, Updated: a.Updated}
		for _, who := range a.Reviewers {
			if strings.Contains(who, "/") {
				who = "@" + who
			}
			r.Reviewers = append(r.Reviewers, registry.PRReviewer{Login: who, State: "requested"})
		}
		for _, rv := range a.Reviews {
			r.Reviewers = append(r.Reviewers, registry.PRReviewer{Login: rv.User, State: reviewWord(rv.State)})
		}
		if a.Via != "" {
			r.Via = "@" + a.Via
		}
		switch {
		case a.Requested && a.Mine == "":
			r.State = registry.ReviewNew
		case a.Requested:
			r.State = registry.ReviewReRequested
		case a.Mine == gh.ReviewChangesRequested:
			r.State = registry.ReviewChanges
		case a.Mine == gh.ReviewApproved:
			r.State = registry.ReviewApproved
		default:
			r.State = registry.ReviewCommented
		}
		out = append(out, r)
	}
	return out, nil
}

// reviewWord is a GitHub review's state as a reviewer's is said: "approved", "changes" or "commented".
func reviewWord(state string) string {
	return cmp.Or(map[string]string{gh.ReviewApproved: "approved", gh.ReviewChangesRequested: "changes"}[state], "commented")
}

// ciWord is a check state as a review request says it.
func ciWord(state string) string {
	switch state {
	case gh.CheckPass:
		return "ok"
	case gh.CheckFail:
		return "failed"
	case gh.CheckPending:
		return "running"
	case gh.CheckSkipped:
		return "skipped"
	}
	return ""
}

func (x *GitHubHost) lookup(ctx context.Context, s registry.Site, url string) (registry.ReviewDetail, error) {
	w, err := x.host()
	if err != nil {
		return registry.ReviewDetail{}, err
	}
	p, err := w.PRReview(ctx, host.PRReviewReq{URL: url, Host: s.Host, Token: x.token(s)})
	if err != nil {
		return registry.ReviewDetail{}, err
	}
	repo, n, _ := gh.ParsePR(url)
	d := registry.ReviewDetail{Request: registry.ReviewRequest{URL: cmp.Or(p.URL, url), Repo: repo.String(), Number: n, Title: p.Title, Author: p.Author,
		Draft: p.Draft, Adds: p.Additions, Dels: p.Deletions, Files: p.ChangedFiles}, Base: p.Base, Head: p.Head, Commit: p.HeadSHA, Labels: p.Labels, Body: p.Body,
		Clone: "https://github.com/" + repo.String() + ".git", State: p.State}
	if p.Merged {
		d.State = "merged"
	}
	if p.Mergeable != nil {
		d.Mergeable = "conflicts"
		if *p.Mergeable {
			d.Mergeable = "yes"
			if p.MergeState == "behind" {
				d.Mergeable += " · behind " + p.Base
			}
		}
	}
	states := gh.ReviewStates(p.Reviews)
	for _, who := range p.Reviewers {
		d.Reviewers = append(d.Reviewers, registry.PRReviewer{Login: who, State: "requested"})
	}
	var done []string
	for who := range states {
		done = append(done, who)
	}
	sort.Strings(done)
	for _, who := range done {
		d.Reviewers = append(d.Reviewers, registry.PRReviewer{Login: who, State: reviewWord(states[who])})
	}
	for _, f := range p.Files {
		d.Files = append(d.Files, registry.PRFileChange{Path: f.Path, Adds: f.Additions, Dels: f.Deletions})
	}
	for _, c := range p.Checks {
		d.Checks = append(d.Checks, CheckOf(c))
	}
	d.Events = prEvents(p)
	d.Reactions = reactionsOf(p.Reactions)
	return d, nil
}

// reactionsOf is GitHub's reactions as emoji and counts, in the order GitHub shows them.
func reactionsOf(r gh.Reactions) []registry.PRReaction {
	var out []registry.PRReaction
	for _, name := range gh.ReactionNames {
		if n := r[name]; n > 0 {
			out = append(out, registry.PRReaction{Emoji: gh.ReactionEmoji[name], Count: n})
		}
	}
	return out
}

// prEvents is a PR's history, oldest first: its commits, its reviews, and the comments in its conversation and on its
// lines. A review that only carries line comments is left to them.
func prEvents(p host.PRReview) []registry.PREvent {
	var out []registry.PREvent
	for _, c := range p.Commits {
		subject, _, _ := strings.Cut(c.Message, "\n")
		out = append(out, registry.PREvent{Kind: registry.PREventPush, Who: c.Author, At: c.Date, Text: subject, Ref: c.SHA})
	}
	for _, r := range p.Reviews {
		state := map[string]string{gh.ReviewApproved: "approved", gh.ReviewChangesRequested: "changes", gh.ReviewCommented: "commented",
			gh.ReviewDismissed: "dismissed"}[r.State]
		if state == "" || state == "commented" && strings.TrimSpace(r.Body) == "" {
			continue
		}
		out = append(out, registry.PREvent{Kind: registry.PREventReview, Who: r.User, At: r.Submitted, Text: r.Body, State: state, URL: r.URL})
	}
	for _, c := range p.Comments {
		out = append(out, registry.PREvent{Kind: registry.PREventComment, Who: c.User, At: c.Created, Text: c.Body, URL: c.URL,
			Reactions: reactionsOf(c.Reactions)})
	}
	for _, c := range p.LineComments {
		ref := c.Path
		if line := cmp.Or(c.Line, c.OriginalLine); line > 0 {
			ref = fmt.Sprintf("%s:%d", c.Path, line)
		}
		out = append(out, registry.PREvent{Kind: registry.PREventComment, Who: c.User, At: c.Created, Text: c.Body, Ref: ref, URL: c.URL,
			Reactions: reactionsOf(c.Reactions)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// submit posts the user's review of a GitHub PR, its comments on the lines they're about.
func (x *GitHubHost) submit(ctx context.Context, s registry.Site, url string, r registry.ReviewSubmit) error {
	w, err := x.host()
	if err != nil {
		return err
	}
	event := map[string]string{registry.VerdictApprove: "APPROVE", registry.VerdictChanges: "REQUEST_CHANGES", registry.VerdictComment: "COMMENT"}[r.Event]
	if event == "" {
		return fmt.Errorf("no such review as %q", r.Event)
	}
	in := gh.ReviewInput{Event: event, Body: r.Body, Commit: r.Commit}
	for _, c := range r.Comments {
		side := "RIGHT"
		if c.Old {
			side = "LEFT"
		}
		in.Comments = append(in.Comments, gh.ReviewComment{Path: c.Path, Line: c.Line, OriginalLine: c.StartLine, Side: side, Body: c.Body})
	}
	return w.SubmitPRReview(ctx, host.PRSubmitReq{URL: url, Host: s.Host, Token: x.token(s), In: in})
}

// react reacts to a GitHub PR with one of the emoji GitHub offers.
func (x *GitHubHost) react(ctx context.Context, s registry.Site, url, emoji string) error {
	name := gh.ReactionName(emoji)
	if name == "" {
		return fmt.Errorf("GitHub has no %s reaction", emoji)
	}
	w, err := x.host()
	if err != nil {
		return err
	}
	return w.ReactPR(ctx, host.PRReactReq{URL: url, Host: s.Host, Token: x.token(s), Content: name})
}

func (x *GitHubHost) unreact(ctx context.Context, s registry.Site, url, emoji string) error {
	name := gh.ReactionName(emoji)
	if name == "" {
		return nil // never reacted with it: GitHub has no such reaction
	}
	w, err := x.host()
	if err != nil {
		return err
	}
	return w.UnreactPR(ctx, host.PRReactReq{URL: url, Host: s.Host, Token: x.token(s), Content: name})
}

// githubCheckout is where a review fetches a GitHub PR from: the repo its URL names, and pull/N/head.
func githubCheckout(_ context.Context, _ registry.Site, url string) (registry.PRCheckout, error) {
	repo, n, err := gh.ParsePR(url)
	if err != nil {
		return registry.PRCheckout{}, err
	}
	return registry.PRCheckout{Remote: repo.String(), Ref: fmt.Sprintf("pull/%d/head", n)}, nil
}

// CheckOf is a GitHub check as a code host tells it.
func CheckOf(c gh.Check) registry.Check {
	out := registry.Check{Name: c.Name, State: ciWord(c.State()), URL: c.URL, Started: c.Started, Completed: c.Completed}
	if out.State == registry.CheckRunning && c.Status != "in_progress" {
		out.State = registry.CheckQueued
	}
	return out
}

// prOf is a GitHub PR as a code host tells it.
func prOf(p *gh.PR) *registry.PR {
	if p == nil {
		return nil
	}
	return &registry.PR{Number: p.Number, ID: p.NodeID, Title: p.Title, Body: p.Body, URL: p.URL, Author: p.Author, State: p.State,
		Draft: p.Draft, Merged: p.Merged, Branch: p.Head, Base: p.Base, Head: p.SHA, Reviewers: p.Reviewers}
}

// ghPR is a code host's PR as gh takes it.
func ghPR(p registry.PR) *gh.PR {
	return &gh.PR{Number: p.Number, NodeID: p.ID, Title: p.Title, Body: p.Body, URL: p.URL, State: p.State, Draft: p.Draft, Merged: p.Merged,
		Head: p.Branch, Base: p.Base, SHA: p.Head}
}

// req is a call on a project's GitHub repo.
func (x *GitHubHost) req(r registry.CodeRepo) host.GitHubReq {
	return host.GitHubReq{Session: core.SessionID(r.Session), Project: r.Project, Host: r.Site.Host, Repo: r.Repo, Token: x.token(r.Site)}
}

// token is the site's token, wherever Oriel found it; "" leaves gh to its own login.
func (x *GitHubHost) token(s registry.Site) string {
	if s.Secret == "" {
		return ""
	}
	v, _ := x.h.Secrets().Secret(s.Secret)
	return v
}

func (x *GitHubHost) forBranch(ctx context.Context, r registry.CodeRepo, branch string) (*registry.PR, error) {
	w, err := x.host()
	if err != nil {
		return nil, err
	}
	q := x.req(r)
	q.Branch = branch
	pr, err := w.GitHubPRFor(ctx, q)
	return prOf(pr), err
}

func (x *GitHubHost) create(ctx context.Context, r registry.CodeRepo, n registry.NewPR) (*registry.PR, error) {
	w, err := x.host()
	if err != nil {
		return nil, err
	}
	q := x.req(r)
	q.New = gh.NewPR{Title: n.Title, Body: n.Body, Head: n.Branch, Base: n.Base, Draft: n.Draft}
	pr, err := w.GitHubCreate(ctx, q)
	return prOf(pr), err
}

func (x *GitHubHost) edit(ctx context.Context, r registry.CodeRepo, pr registry.PR, title, body string) (*registry.PR, error) {
	w, err := x.host()
	if err != nil {
		return nil, err
	}
	q := x.req(r)
	q.PR, q.Title, q.Body = ghPR(pr), title, body
	out, err := w.GitHubEdit(ctx, q)
	return prOf(out), err
}

func (x *GitHubHost) ready(ctx context.Context, r registry.CodeRepo, pr registry.PR) error {
	w, err := x.host()
	if err != nil {
		return err
	}
	q := x.req(r)
	q.PR = ghPR(pr)
	return w.GitHubReady(ctx, q)
}

func (x *GitHubHost) reviewers(ctx context.Context, r registry.CodeRepo, pr registry.PR, who []string) error {
	w, err := x.host()
	if err != nil {
		return err
	}
	q := x.req(r)
	q.PR, q.Reviewers = ghPR(pr), who
	return w.GitHubReviewers(ctx, q)
}

// checks are the checks on the PR's branch, the first failing Actions job's with its log's failing line.
func (x *GitHubHost) checks(ctx context.Context, r registry.CodeRepo, pr registry.PR) ([]registry.Check, error) {
	w, err := x.host()
	if err != nil {
		return nil, err
	}
	q := x.req(r)
	q.Branch = pr.Branch
	cs, err := w.GitHubChecks(ctx, q)
	if err != nil {
		return nil, err
	}
	out := make([]registry.Check, 0, len(cs.Checks))
	told := false
	for _, c := range cs.Checks {
		rc := CheckOf(c)
		if !told && rc.State == registry.CheckFailed && c.Actions {
			rc.Detail, told = cs.FailLine, true
		}
		out = append(out, rc)
	}
	return out, nil
}

func (x *GitHubHost) mergeState(ctx context.Context, r registry.CodeRepo, pr registry.PR) (gh.MergeState, error) {
	w, err := x.host()
	if err != nil {
		return gh.MergeState{}, err
	}
	q := x.req(r)
	q.PR = ghPR(pr)
	return w.GitHubMergeState(ctx, q)
}

func (x *GitHubHost) mergeMethods(ctx context.Context, r registry.CodeRepo, pr registry.PR) (registry.MergeMethods, error) {
	m, err := x.mergeState(ctx, r, pr)
	return registry.MergeMethods{Allowed: m.Methods, Default: m.Default}, err
}

// blockers are what GitHub says of merging now: unresolved review threads, changes still asked for, and the PR's
// mergeStateStatus. A draft, a conflict and missing required reviews GitHub refuses outright.
func (x *GitHubHost) blockers(ctx context.Context, r registry.CodeRepo, pr registry.PR) ([]registry.MergeBlocker, error) {
	m, err := x.mergeState(ctx, r, pr)
	if err != nil {
		return nil, err
	}
	var out []registry.MergeBlocker
	if m.Unresolved > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockThreads, Count: m.Unresolved,
			Text: kit.Counted(m.Unresolved, "reviewer comment") + " unresolved", URL: pr.URL + "/files"})
	}
	if len(m.Changes) > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockChanges, Count: len(m.Changes),
			Text: strings.Join(m.Changes, ", ") + " asked for changes", URL: pr.URL})
	}
	if m.Draft {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockDraft, Text: "it's a draft: mark it ready first", Hard: true})
	}
	if m.Mergeable == "CONFLICTING" {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockConflicts, Text: "it conflicts with " + cmp.Or(pr.Base, "its base"), Hard: true})
	}
	switch {
	case m.Status == "BEHIND":
		out = append(out, registry.MergeBlocker{Kind: registry.BlockBehind, Text: "it's behind " + cmp.Or(pr.Base, "its base")})
	case m.Status == "BLOCKED" && m.Decision == "REVIEW_REQUIRED":
		out = append(out, registry.MergeBlocker{Kind: registry.BlockReviews, Text: "the reviews the branch needs aren't in", Hard: true, URL: pr.URL})
	}
	return out, nil
}

// merge merges at the head the user saw, then deletes the branch if asked.
func (x *GitHubHost) merge(ctx context.Context, r registry.CodeRepo, pr registry.PR, o registry.MergeOpts) error {
	w, err := x.host()
	if err != nil {
		return err
	}
	q := x.req(r)
	q.PR, q.Merge = ghPR(pr), gh.Merge{Method: o.Method, SHA: o.Head, Title: o.Title, Message: o.Body}
	if err := w.GitHubMerge(ctx, q); err != nil {
		return err
	}
	if !o.DeleteBranch || pr.Branch == "" {
		return nil
	}
	q.Branch = pr.Branch
	return w.GitHubDeleteBranch(ctx, q)
}
