package host

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
)

// GitHub review mirroring (ui.md 06, RV-4): a session's PRs' review threads and conversation become threads with
// origin github, which reach the agent like any comment. On them the agent only drafts; the user forwards a draft,
// and the host posts it with the user's own GitHub login. A forwarded comment carries its GitHub ID, so it never
// comes back as a new comment (echo reconciliation); edits, deletions and resolutions on GitHub update the mirror.

// PRsReq is a search of GitHub's pull requests: gh.SearchMine, gh.SearchReview.
type PRsReq struct {
	Query string `json:"query"`
}

// SearchPRs finds pull requests across GitHub as the user's gh is signed in.
func (w *Host) SearchPRs(ctx context.Context, r PRsReq) (_ []gh.Found, err error) {
	defer w.recover("SearchPRs", &err)
	if strings.TrimSpace(r.Query) == "" {
		return nil, errors.New("no search")
	}
	return w.github(gitx.Repo{}).SearchPRs(ctx, r.Query)
}

// ReviewQueue is the user's review queue on GitHub, as their gh is signed in (gh.API.ReviewQueue).
func (w *Host) ReviewQueue(ctx context.Context, r ReviewQueueReq) (_ []gh.Asked, err error) {
	defer w.recover("ReviewQueue", &err)
	return w.githubOn(gitx.Repo{}, r.Host, r.Token).ReviewQueue(ctx)
}

// ReviewQueueReq names the GitHub server: github.com if Host is "".
type ReviewQueueReq struct {
	Host  string `json:"host,omitempty"`
	Token string `json:"-"`
}

// PRReview is a pull request in full, as the Reviews page shows it.
type PRReview struct {
	gh.PRDetails
	Files   []gh.PRFile `json:"files,omitempty"`
	Reviews []gh.Review `json:"reviews,omitempty"`
	Checks  []gh.Check  `json:"checks,omitempty"`
}

// PRReviewReq names a pull request by its URL, and its GitHub server's hostname.
type PRReviewReq struct {
	URL   string `json:"url"`
	Host  string `json:"host,omitempty"`
	Token string `json:"-"`
}

// PRReview reads a pull request in full: its details, files, reviews and the checks on its head.
func (w *Host) PRReview(ctx context.Context, r PRReviewReq) (_ PRReview, err error) {
	defer w.recover("PRReview", &err)
	repo, n, err := gh.ParsePR(r.URL)
	if err != nil {
		return PRReview{}, err
	}
	c := w.githubOn(gitx.Repo{}, r.Host, r.Token)
	var out PRReview
	if out.PRDetails, err = c.PRDetails(ctx, repo, n); err != nil {
		return PRReview{}, err
	}
	if out.Files, err = c.PRFiles(ctx, repo, n); err != nil {
		return PRReview{}, err
	}
	if out.Reviews, err = c.Reviews(ctx, repo, n); err != nil {
		return PRReview{}, err
	}
	if out.HeadSHA != "" {
		out.Checks, _ = c.Checks(ctx, repo, out.HeadSHA)
	}
	return out, nil
}

// PRSubmitReq is the user's review of a pull request on GitHub: In's event, body, head and comments.
type PRSubmitReq struct {
	URL   string         `json:"url"`
	Host  string         `json:"host,omitempty"`
	Token string         `json:"-"`
	In    gh.ReviewInput `json:"in"`
}

// SubmitPRReview submits the user's review of a pull request: never an agent's (NoPosting).
func (w *Host) SubmitPRReview(ctx context.Context, r PRSubmitReq) (err error) {
	defer w.recover("SubmitPRReview", &err)
	repo, n, err := gh.ParsePR(r.URL)
	if err != nil {
		return err
	}
	return w.githubOn(gitx.Repo{}, r.Host, r.Token).SubmitReview(ctx, repo, n, r.In)
}

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
	Head    string            `json:"head,omitempty"` // a reviewed PR's head commit on GitHub
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
	mu2     sync.Mutex
	me      string // the user's GitHub login
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

// SyncGitHub mirrors the review of each of a session's projects' PRs into its threads.
func (w *Host) SyncGitHub(ctx context.Context, r MirrorReq) (_ Mirror, err error) {
	defer w.recover("SyncGitHub", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return Mirror{}, err
	}
	w.mirror.mu.Lock()
	defer w.mirror.mu.Unlock()
	out := Mirror{Synced: w.opts.Now().UTC()}
	kick := false
	for _, p := range se.Projects {
		reviewed := se.PR != "" && p.Detached && strings.HasPrefix(p.Base, "refs/oriel/pr/")
		if !reviewed && (p.ReadOnly || p.Detached || p.Branch == "") {
			continue
		}
		env, err := sandbox.GitEnv(p.Dir)
		if err != nil {
			continue
		}
		repo := gitx.Repo{Dir: p.Dir, Env: env}
		var ghRepo gh.Repo
		var pr *gh.PR
		if reviewed {
			if pr, ghRepo, err = w.reviewedPR(ctx, repo, se.PR); err != nil {
				out.Errors = append(out.Errors, p.Name+": "+err.Error())
				continue
			}
		} else {
			var ok bool
			if ghRepo, ok = gh.ParseRepo(repo.RemoteURL(ctx)); !ok {
				continue
			}
			g := w.githubFor(ctx, se.ID, p.Name, repo, ghRepo, "", "", p.Branch, r.Fresh)
			if g.err != "" {
				out.Errors = append(out.Errors, p.Name+": "+g.err)
				continue
			}
			if pr = g.pr; pr == nil {
				continue
			}
		}
		mp, k, err := w.mirrorPR(ctx, se.ID, p, repo, ghRepo, pr, r.Trust)
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
			if h, err := w.sessionFor(r.Session, true); err == nil {
				h.kick()
			}
		}
	}
	w.restatus(r.Session)
	return out, nil
}

// reviewedPR is the pull request a review session reads, as GitHub has it now.
func (w *Host) reviewedPR(ctx context.Context, repo gitx.Repo, url string) (*gh.PR, gh.Repo, error) {
	ghRepo, n, err := gh.ParsePR(url)
	if err != nil {
		return nil, ghRepo, err
	}
	d, err := w.github(repo).PRDetails(ctx, ghRepo, n)
	if err != nil {
		return nil, ghRepo, err
	}
	return &gh.PR{Number: n, URL: url, Author: d.Author, SHA: d.HeadSHA}, ghRepo, nil
}

// mirrorPR brings one PR's review threads and conversation into the session; kick says the agent has something new.
func (w *Host) mirrorPR(ctx context.Context, id core.SessionID, p core.Project, repo gitx.Repo, ghRepo gh.Repo, pr *gh.PR, trust Trust) (MirrorPR, bool, error) {
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
	mp := MirrorPR{Project: p.Name, Repo: ghRepo.String(), Number: pr.Number, URL: pr.URL, Reviews: states, Head: pr.SHA}
	trusted := w.truster(ctx, c, ghRepo, trust)
	se, err := w.session(id)
	if err != nil {
		return mp, false, err
	}
	me := ""
	if se.PR != "" {
		me = w.githubMe(ctx, c)
	}
	msg := func(id int64, user, assoc, body string, at time.Time) mirrorMsg {
		return mirrorMsg{num: id, user: user, body: body, at: at, trusted: trusted(user, assoc), own: me != "" && user == me}
	}

	byRoot := map[int64][]gh.ReviewComment{}
	var roots []int64
	for _, rc := range comments {
		root := cmp.Or(rc.InReplyTo, rc.ID)
		if byRoot[root] == nil {
			roots = append(roots, root)
		}
		byRoot[root] = append(byRoot[root], rc)
	}
	var threads []mirrored
	for _, root := range roots {
		cs := byRoot[root]
		first := cs[0]
		if first.ID != root {
			continue // its first comment was deleted
		}
		var msgs []mirrorMsg
		for _, rc := range cs {
			msgs = append(msgs, msg(rc.ID, rc.User, rc.Association, rc.Body, rc.Created))
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
		threads = append(threads, mirrored{ref: core.HostThread{Project: p.Name, Repo: mp.Repo, PR: pr.Number, Root: root, Reviewer: first.User,
			State: states[first.User], URL: first.URL, Outdated: first.Line == 0}, anchor: a, msgs: msgs, res: res, into: sentFrom(se, first)})
	}

	var convo []mirrorMsg
	for _, ic := range issue {
		convo = append(convo, msg(ic.ID, ic.User, ic.Association, ic.Body, ic.Created))
	}
	for _, rv := range reviews {
		if strings.TrimSpace(rv.Body) != "" {
			convo = append(convo, msg(rv.ID, rv.User, rv.Association, rv.Body, rv.Submitted))
		}
	}
	slices.SortStableFunc(convo, func(a, b mirrorMsg) int { return a.at.Compare(b.at) })
	ref := core.HostThread{Project: p.Name, Repo: mp.Repo, PR: pr.Number, URL: pr.URL}
	if len(convo) > 0 {
		ref.Reviewer = convo[0].user
		ref.State = states[ref.Reviewer]
	}
	threads = append(threads, mirrored{ref: ref, msgs: convo})
	kick, err := w.applyMirror(id, "", mp.Repo, pr.Number, threads)
	return mp, kick, err
}

// sentFrom is the thread of the review's comment that GitHub's review comment c was posted from: the same words, on its
// file and line.
func sentFrom(se core.SessionState, c gh.ReviewComment) core.ThreadID {
	if se.Review == nil || c.InReplyTo != 0 {
		return ""
	}
	for id, pk := range se.Review.Picks {
		if pk.Sent == "" || strings.TrimSpace(pk.Sent) != strings.TrimSpace(c.Body) {
			continue
		}
		if t, ok := se.Thread(core.ThreadID(id)); ok && t.Hosted == nil && t.Anchor != nil && t.Anchor.File == c.Path {
			return t.ID
		}
	}
	return ""
}

// githubMe is who the user is on GitHub, asked once.
func (w *Host) githubMe(ctx context.Context, c gh.Client) string {
	w.mirror.mu2.Lock()
	defer w.mirror.mu2.Unlock()
	if w.mirror.me == "" {
		w.mirror.me, _ = c.Me(ctx)
	}
	return w.mirror.me
}

// truster says whose text the agent may read: a trusted user, or a member of a trusted org (the repo's owner when
// none is set).
func (w *Host) truster(ctx context.Context, c gh.Client, repo gh.Repo, trust Trust) func(user, assoc string) bool {
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
func (w *Host) ForwardGitHub(ctx context.Context, r ForwardReq) (_ core.Comment, err error) {
	defer w.recover("ForwardGitHub", &err)
	w.mirror.mu.Lock()
	defer w.mirror.mu.Unlock()
	t, body, err := w.DraftOf(r)
	if err != nil {
		return core.Comment{}, err
	}
	if t.Hosted.Host != "" {
		return core.Comment{}, errors.New("this thread isn't from GitHub")
	}
	_, _, repo, err := w.projectRepo(ctx, r.Session, t.Hosted.Project)
	if err != nil {
		return core.Comment{}, err
	}
	owner, name, _ := strings.Cut(t.Hosted.Repo, "/")
	ghRepo, c := gh.Repo{Owner: owner, Name: name}, w.github(repo)
	var posted int64
	if t.Hosted.Root != 0 {
		rc, err := c.ReplyReview(ctx, ghRepo, t.Hosted.PR, t.Hosted.Root, body)
		if err != nil {
			return core.Comment{}, err
		}
		posted = rc.ID
	} else {
		ic, err := c.Comment(ctx, ghRepo, t.Hosted.PR, body)
		if err != nil {
			return core.Comment{}, err
		}
		posted = ic.ID
	}
	return w.forwarded(r.Session, t.ID, r.Comment, posted, "")
}

// EditDraft rewrites a draft reply before it's forwarded.
func (w *Host) EditDraft(r DraftEditReq) (err error) {
	defer w.recover("EditDraft", &err)
	body := strings.TrimSpace(r.Body)
	if body == "" {
		return errors.New("the draft is empty")
	}
	_, err = w.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
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

// SetReviewDraft keeps the review the user is writing of a review session's pull request.
func (w *Host) SetReviewDraft(r core.ReviewDrafted) (err error) {
	defer w.recover("SetReviewDraft", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	if se.PR == "" {
		return fmt.Errorf("session %s isn't reviewing a pull request", r.Session)
	}
	return w.emit(r)
}

// ResolveReq resolves a thread mirrored from GitHub there too, or reopens it, as the user did in Oriel.
type ResolveReq struct {
	Session  core.SessionID `json:"session"`
	Thread   core.ThreadID  `json:"thread"`
	Resolved bool           `json:"resolved"`
}

// ResolveGitHub resolves or reopens a review thread on GitHub, and records that it is.
func (w *Host) ResolveGitHub(ctx context.Context, r ResolveReq) (err error) {
	defer w.recover("ResolveGitHub", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	t, ok := se.Thread(r.Thread)
	if !ok || t.Hosted == nil || t.Hosted.Host != "" || t.Hosted.Root == 0 {
		return fmt.Errorf("thread %s isn't a GitHub review thread", r.Thread)
	}
	_, _, repo, err := w.projectRepo(ctx, r.Session, t.Hosted.Project)
	if err != nil {
		return err
	}
	owner, name, _ := strings.Cut(t.Hosted.Repo, "/")
	if err := w.github(repo).ResolveThread(ctx, gh.Repo{Owner: owner, Name: name}, t.Hosted.PR, t.Hosted.Root, r.Resolved); err != nil {
		return err
	}
	return w.MarkHostResolved(r)
}

// MarkHostResolved records that a mirrored thread is resolved, or open, on its code host: once the user's resolve has
// reached it.
func (w *Host) MarkHostResolved(r ResolveReq) (err error) {
	defer w.recover("MarkHostResolved", &err)
	_, err = w.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
		if t.Hosted == nil {
			return fmt.Errorf("thread %s isn't from a code host", r.Thread)
		}
		g := *t.Hosted
		g.Resolved = r.Resolved
		t.Hosted = &g
		return nil
	})
	return err
}
