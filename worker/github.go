package worker

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/wire"
)

// GitHub review mirroring (ui.md 06, RV-4): a session's PRs' review threads and conversation become threads with
// origin github, which reach the agent like any comment. On them the agent only drafts; the user forwards a draft,
// and the worker posts it with the user's own GitHub login. A forwarded comment carries its GitHub ID, so it never
// comes back as a new comment (echo reconciliation); edits, deletions and resolutions on GitHub update the mirror.

// What the app asks of the mirror.
const (
	MethodGitHubSync    = "github.sync"
	MethodGitHubForward = "github.forward"
	MethodGitHubDraft   = "github.draft"
)

// NoPosting is why an agent's attempt to post to a pull request's review or conversation is refused.
const NoPosting = "Oriel: agents never post to GitHub reviews. Answer the thread with mcp__oriel__reply: on a GitHub " +
	"thread your reply is a draft, and the user forwards it."

// Trust is whose GitHub text the agent may read (crit's trusted orgs): members of Orgs and the Users named. With
// neither set, the repository's owner is the trusted org.
type Trust struct {
	Orgs  []string `json:"orgs,omitempty"`
	Users []string `json:"users,omitempty"`
}

// MirrorReq syncs a session's PRs' reviews; Fresh asks GitHub for the PR again rather than the last 30 s's.
type MirrorReq struct {
	Session core.SessionID `json:"session"`
	Fresh   bool           `json:"fresh,omitempty"`
	Trust   Trust          `json:"trust,omitzero"`
}

// Mirror is what a sync found.
type Mirror struct {
	Synced time.Time  `json:"synced"`
	PRs    []MirrorPR `json:"prs,omitempty"`
	Errors []string   `json:"errors,omitempty"`
}

// MirrorPR is one project's PR and where each reviewer stands (gh.ReviewStates).
type MirrorPR struct {
	Project string            `json:"project"`
	Repo    string            `json:"repo"`
	Number  int               `json:"number"`
	URL     string            `json:"url,omitempty"`
	Reviews map[string]string `json:"reviews,omitempty"`
}

// ForwardReq forwards an agent's draft reply on a GitHub thread to GitHub.
type ForwardReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
}

// DraftEditReq rewrites a draft reply before it's forwarded.
type DraftEditReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
	Body    string         `json:"body"`
}

// mirrorState serialises syncs and forwards, so a forward's echo is always recognised; and remembers org members.
type mirrorState struct {
	mu      sync.Mutex
	members map[string]bool // org/login
}

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

// ghMsg is a comment from GitHub, of whichever kind.
type ghMsg struct {
	id          int64
	user, assoc string
	body        string
	at          time.Time
}

// SyncGitHub mirrors the review of each of a session's projects' PRs into its threads.
func (w *Worker) SyncGitHub(ctx context.Context, r MirrorReq) (Mirror, error) {
	se, err := w.session(r.Session)
	if err != nil {
		return Mirror{}, err
	}
	w.mirror.mu.Lock()
	defer w.mirror.mu.Unlock()
	out := Mirror{Synced: w.opts.Now().UTC()}
	kick := false
	for _, p := range se.Projects {
		if p.ReadOnly || p.Detached || p.Branch == "" {
			continue
		}
		env, err := sandbox.GitEnv(p.Dir)
		if err != nil {
			continue
		}
		repo := gitx.Repo{Dir: p.Dir, Env: env}
		ghRepo, ok := gh.ParseRepo(repo.RemoteURL(ctx))
		if !ok {
			continue
		}
		g := w.githubFor(ctx, se.ID, p.Name, repo, ghRepo, p.Branch, r.Fresh)
		if g.err != "" {
			out.Errors = append(out.Errors, p.Name+": "+g.err)
			continue
		}
		if g.pr == nil {
			continue
		}
		mp, k, err := w.mirrorPR(ctx, se.ID, p, repo, ghRepo, g.pr, r.Trust)
		if err != nil {
			out.Errors = append(out.Errors, p.Name+": "+err.Error())
			continue
		}
		out.PRs = append(out.PRs, mp)
		kick = kick || k
	}
	if kick {
		se, _ = w.session(r.Session)
		if se.Agent.Provider != "" && !se.Archived {
			if h, err := w.hostFor(r.Session, true); err == nil {
				h.kick()
			}
		}
	}
	w.restatus(r.Session)
	return out, nil
}

// mirrorPR brings one PR's review threads and conversation into the session; kick says the agent has something new.
func (w *Worker) mirrorPR(ctx context.Context, id core.SessionID, p core.Project, repo gitx.Repo, ghRepo gh.Repo, pr *gh.PR, trust Trust) (MirrorPR, bool, error) {
	c := w.github(repo)
	comments, err := c.ReviewComments(ctx, ghRepo, pr.Number)
	if err != nil {
		return MirrorPR{}, false, err
	}
	reviews, err := c.Reviews(ctx, ghRepo, pr.Number)
	if err != nil {
		return MirrorPR{}, false, err
	}
	issue, err := c.IssueComments(ctx, ghRepo, pr.Number)
	if err != nil {
		return MirrorPR{}, false, err
	}
	resolved, err := c.ResolvedThreads(ctx, ghRepo, pr.Number)
	if err != nil {
		resolved = nil // unknown: leave each thread's status as it is
	}
	states := gh.ReviewStates(reviews)
	mp := MirrorPR{Project: p.Name, Repo: ghRepo.String(), Number: pr.Number, URL: pr.URL, Reviews: states}
	trusted := w.truster(ctx, c, ghRepo, trust)

	se, err := w.session(id)
	if err != nil {
		return mp, false, err
	}
	now := w.opts.Now().UTC()
	kick := false
	apply := func(root int64, ref core.GitHubThread, anchor *core.Anchor, msgs []ghMsg, res *bool) {
		i := slices.IndexFunc(se.Threads, func(t core.Thread) bool {
			return t.GitHub != nil && t.GitHub.Repo == ref.Repo && t.GitHub.PR == ref.PR && t.GitHub.Root == root
		})
		var t core.Thread
		fresh := i < 0
		if fresh {
			if len(msgs) == 0 {
				return
			}
			t = core.Thread{ID: core.ThreadID(newID(8)), Origin: core.OriginGitHub, Status: core.ThreadOpen, Anchor: anchor, Created: now}
		} else {
			t = se.Threads[i]
			t.Comments = slices.Clone(t.Comments)
			g := *t.GitHub
			t.GitHub = &g
		}
		before, _ := json.Marshal(t)
		was := t.GitHub != nil && t.GitHub.Resolved
		ref.Resolved = was
		if res != nil {
			ref.Resolved = *res
		}
		t.GitHub = &ref
		mergeMsgs(&t, msgs, trusted)
		switch {
		case ref.Resolved && (fresh || !was) && t.Open():
			t.Status, t.Busy = core.ThreadResolved, false
		case !ref.Resolved && was && t.Status == core.ThreadResolved:
			t.Status = core.ThreadOpen
		}
		if after, _ := json.Marshal(t); string(after) == string(before) {
			return
		}
		t.Updated = now
		if err := w.emit(core.ThreadUpdated{Session: id, Thread: t}); err == nil && t.Open() && len(t.Undelivered()) > 0 {
			kick = true
		}
	}

	threads := map[int64][]gh.ReviewComment{}
	var roots []int64
	for _, rc := range comments {
		root := cmp.Or(rc.InReplyTo, rc.ID)
		if threads[root] == nil {
			roots = append(roots, root)
		}
		threads[root] = append(threads[root], rc)
	}
	for _, root := range roots {
		cs := threads[root]
		first := cs[0]
		if first.ID != root {
			continue // its first comment was deleted
		}
		var msgs []ghMsg
		for _, rc := range cs {
			msgs = append(msgs, ghMsg{id: rc.ID, user: rc.User, assoc: rc.Association, body: rc.Body, at: rc.Created})
		}
		line, side := first.Line, ""
		if line == 0 {
			line = first.OriginalLine
		}
		if first.Side == "LEFT" {
			side = "old"
		}
		a := &core.Anchor{Project: p.Name, File: first.Path, Line: max(line, 1), Side: side}
		if side == "" {
			a.Text = anchorText(se, a)
		}
		var res *bool
		if resolved != nil {
			v := resolved[root]
			res = &v
		}
		apply(root, core.GitHubThread{Project: p.Name, Repo: mp.Repo, PR: pr.Number, Root: root, Reviewer: first.User, State: states[first.User],
			URL: first.URL, Outdated: first.Line == 0}, a, msgs, res)
	}
	// A mirrored review thread whose first comment is gone from GitHub is dismissed.
	for _, t := range se.Threads {
		if g := t.GitHub; g != nil && g.Repo == mp.Repo && g.PR == pr.Number && g.Root != 0 && threads[g.Root] == nil && t.Status != core.ThreadDismissed {
			t.Status, t.Busy, t.Updated = core.ThreadDismissed, false, now
			_ = w.emit(core.ThreadUpdated{Session: id, Thread: t})
		}
	}

	var convo []ghMsg
	for _, ic := range issue {
		convo = append(convo, ghMsg{id: ic.ID, user: ic.User, assoc: ic.Association, body: ic.Body, at: ic.Created})
	}
	for _, rv := range reviews {
		if strings.TrimSpace(rv.Body) != "" {
			convo = append(convo, ghMsg{id: rv.ID, user: rv.User, assoc: rv.Association, body: rv.Body, at: rv.Submitted})
		}
	}
	slices.SortStableFunc(convo, func(a, b ghMsg) int { return a.at.Compare(b.at) })
	ref := core.GitHubThread{Project: p.Name, Repo: mp.Repo, PR: pr.Number, URL: pr.URL}
	if len(convo) > 0 {
		ref.Reviewer = convo[0].user
		ref.State = states[ref.Reviewer]
	}
	apply(0, ref, nil, convo, nil)
	return mp, kick, nil
}

// mergeMsgs brings a thread's comments in line with GitHub's: new ones added, edited ones changed (and given to the
// agent again), deleted ones taken out. A comment of Oriel's (a forwarded draft) is matched by its GitHub ID, so it
// isn't added again.
func mergeMsgs(t *core.Thread, msgs []ghMsg, trusted func(user, assoc string) bool) {
	seen := map[int64]bool{}
	for _, m := range msgs {
		seen[m.id] = true
		i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return c.GitHubID == m.id })
		if i < 0 {
			t.Comments = append(t.Comments, core.Comment{ID: newID(6), Author: core.AuthorGitHub, Name: m.user, Body: m.body, Time: m.at.UTC(),
				GitHubID: m.id, Redacted: !trusted(m.user, m.assoc)})
			continue
		}
		c := &t.Comments[i]
		if c.Author == core.AuthorGitHub && c.Body != m.body {
			c.Body, c.Edited, c.Delivered = m.body, true, false
			c.Redacted = !trusted(m.user, m.assoc)
		}
	}
	t.Comments = slices.DeleteFunc(t.Comments, func(c core.Comment) bool {
		return c.Author == core.AuthorGitHub && c.GitHubID != 0 && !seen[c.GitHubID]
	})
}

// truster says whose text the agent may read: a trusted user, or a member of a trusted org (the repo's owner when
// none is set).
func (w *Worker) truster(ctx context.Context, c gh.Client, repo gh.Repo, trust Trust) func(user, assoc string) bool {
	orgs := trust.Orgs
	if len(orgs) == 0 && len(trust.Users) == 0 {
		orgs = []string{repo.Owner}
	}
	return func(user, assoc string) bool {
		if slices.ContainsFunc(trust.Users, func(u string) bool { return strings.EqualFold(strings.TrimPrefix(u, "@"), user) }) {
			return true
		}
		for _, org := range orgs {
			if strings.EqualFold(org, repo.Owner) && (assoc == "OWNER" || assoc == "MEMBER") {
				return true
			}
			key := strings.ToLower(org + "/" + user)
			in, ok := w.mirror.members[key]
			if !ok {
				var err error
				if in, err = c.OrgMember(ctx, org, user); err != nil {
					continue // not known: not trusted, and asked again next time
				}
				if w.mirror.members == nil {
					w.mirror.members = map[string]bool{}
				}
				w.mirror.members[key] = in
			}
			if in {
				return true
			}
		}
		return false
	}
}

// ForwardGitHub posts the agent's draft reply to GitHub as the user, once, and marks it sent.
func (w *Worker) ForwardGitHub(ctx context.Context, r ForwardReq) (core.Comment, error) {
	w.mirror.mu.Lock()
	defer w.mirror.mu.Unlock()
	se, err := w.session(r.Session)
	if err != nil {
		return core.Comment{}, err
	}
	t, err := findThread(se, string(r.Thread))
	if err != nil {
		return core.Comment{}, err
	}
	if t.GitHub == nil {
		return core.Comment{}, errors.New("this thread isn't from GitHub")
	}
	i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return c.ID == r.Comment })
	switch {
	case i < 0:
		return core.Comment{}, fmt.Errorf("no comment %s in the thread", r.Comment)
	case t.Comments[i].GitHubID != 0:
		return core.Comment{}, errors.New("this reply is already on GitHub")
	case !t.Comments[i].Draft:
		return core.Comment{}, errors.New("only a draft reply can be forwarded")
	}
	body := strings.TrimSpace(t.Comments[i].Body)
	if body == "" {
		return core.Comment{}, errors.New("the draft is empty")
	}
	_, _, repo, err := w.projectRepo(r.Session, t.GitHub.Project)
	if err != nil {
		return core.Comment{}, err
	}
	owner, name, _ := strings.Cut(t.GitHub.Repo, "/")
	ghRepo, c := gh.Repo{Owner: owner, Name: name}, w.github(repo)
	var posted int64
	if t.GitHub.Root != 0 {
		rc, err := c.ReplyReview(ctx, ghRepo, t.GitHub.PR, t.GitHub.Root, body)
		if err != nil {
			return core.Comment{}, err
		}
		posted = rc.ID
	} else {
		ic, err := c.Comment(ctx, ghRepo, t.GitHub.PR, body)
		if err != nil {
			return core.Comment{}, err
		}
		posted = ic.ID
	}
	var sent core.Comment
	_, err = w.mutateThread(r.Session, t.ID, func(t *core.Thread) error {
		for i := range t.Comments {
			if t.Comments[i].ID == r.Comment {
				t.Comments[i].Draft, t.Comments[i].GitHubID, t.Comments[i].Sent = false, posted, w.opts.Now().UTC()
				sent = t.Comments[i]
			}
		}
		return nil
	})
	w.restatus(r.Session)
	return sent, err
}

// EditDraft rewrites a draft reply before it's forwarded.
func (w *Worker) EditDraft(r DraftEditReq) error {
	body := strings.TrimSpace(r.Body)
	if body == "" {
		return errors.New("the draft is empty")
	}
	_, err := w.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
		for i := range t.Comments {
			if t.Comments[i].ID == r.Comment {
				if !t.Comments[i].Draft {
					return errors.New("only a draft reply can be edited")
				}
				t.Comments[i].Body = body
				return nil
			}
		}
		return fmt.Errorf("no comment %s in the thread", r.Comment)
	})
	return err
}

func (w *Worker) serveGitHub(fn func(string, wire.Handler)) {
	fn(MethodGitHubSync, wire.Func(func(ctx context.Context, r MirrorReq) (Mirror, error) { return w.SyncGitHub(ctx, r) }))
	fn(MethodGitHubForward, wire.Func(func(ctx context.Context, r ForwardReq) (core.Comment, error) { return w.ForwardGitHub(ctx, r) }))
	fn(MethodGitHubDraft, wire.Func(func(_ context.Context, r DraftEditReq) (any, error) { return nil, w.EditDraft(r) }))
}

// SyncGitHub mirrors a session's PRs' reviews into its threads (github.sync).
func (c Client) SyncGitHub(ctx context.Context, r MirrorReq) (Mirror, error) {
	var out Mirror
	return out, c.Conn.Call(ctx, MethodGitHubSync, r, &out)
}

// ForwardGitHub posts a draft reply to GitHub as the user (github.forward).
func (c Client) ForwardGitHub(ctx context.Context, r ForwardReq) (core.Comment, error) {
	var out core.Comment
	return out, c.Conn.Call(ctx, MethodGitHubForward, r, &out)
}

// EditDraft rewrites a draft reply (github.draft).
func (c Client) EditDraft(ctx context.Context, r DraftEditReq) error {
	return c.Conn.Call(ctx, MethodGitHubDraft, r, nil)
}
