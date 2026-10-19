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
		g := w.githubFor(ctx, se.ID, p.Name, repo, ghRepo, "", "", p.Branch, r.Fresh)
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
			if h, err := w.sessionFor(r.Session, true); err == nil {
				h.kick()
			}
		}
	}
	w.restatus(r.Session)
	return out, nil
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
	mp := MirrorPR{Project: p.Name, Repo: ghRepo.String(), Number: pr.Number, URL: pr.URL, Reviews: states}
	trusted := w.truster(ctx, c, ghRepo, trust)
	msg := func(id int64, user, assoc, body string, at time.Time) mirrorMsg {
		return mirrorMsg{num: id, user: user, body: body, at: at, trusted: trusted(user, assoc)}
	}
	se, err := w.session(id)
	if err != nil {
		return mp, false, err
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
			State: states[first.User], URL: first.URL, Outdated: first.Line == 0}, anchor: a, msgs: msgs, res: res})
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
