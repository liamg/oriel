package github

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// Review mirroring for any code host: a PR's review threads and conversation, as GitHub's SyncGitHub or the app (for a
// code host's plugin) reads them, are brought into the session's threads, and the user's forwarded drafts marked sent.

// mirrorMsg is one comment of a mirrored thread: num is GitHub's id, key another host's.
type mirrorMsg struct {
	num     int64
	key     string
	user    string
	body    string
	at      time.Time
	trusted bool // the agent may read its text
	own     bool // the user's own: not given to the agent as theirs to answer
}

// mirrored is a thread as its code host has it.
type mirrored struct {
	ref    core.HostThread
	anchor *core.Anchor
	msgs   []mirrorMsg
	res    *bool         // resolved there; nil when not known
	into   core.ThreadID // the session's thread it was posted from, which it joins
}

// applyMirror brings a PR's threads on host (GitHub when "") in line with threads, and dismisses its mirrored review
// threads the host no longer has; kick says the agent has something new. It holds the threads' edit lock, so the
// agent's own changes to them meanwhile aren't lost.
func (x *GitHub) applyMirror(id core.SessionID, host, repo string, pr int, threads []mirrored) (bool, error) {
	kick := false
	err := x.st.Mutate(id, func(se core.SessionState) error {
		kick = x.mirrorInto(se, host, repo, pr, threads)
		return nil
	})
	return kick, err
}

// mirrorInto is applyMirror on se, under the edit lock.
func (x *GitHub) mirrorInto(se core.SessionState, host, repo string, pr int, threads []mirrored) bool {
	id := se.ID
	now := x.env.Now().UTC()
	origin := core.OriginGitHub
	if host != "" {
		origin = core.OriginCodeHost
	}
	same := func(g *core.HostThread, ref core.HostThread) bool {
		return g != nil && g.Host == host && g.Repo == repo && g.PR == pr && g.Root == ref.Root && g.Key == ref.Key
	}
	kick := false
	seen := map[string]bool{}
	for _, m := range threads {
		ref := m.ref
		ref.Host = host
		if ref.Root != 0 || ref.Key != "" {
			seen[fmt.Sprint(ref.Root, "/", ref.Key)] = true
		}
		i := slices.IndexFunc(se.Threads, func(t core.Thread) bool { return same(t.Hosted, ref) })
		var t core.Thread
		fresh := i < 0
		j := slices.IndexFunc(se.Threads, func(t core.Thread) bool { return m.into != "" && t.ID == m.into })
		switch {
		case fresh && j >= 0:
			t = se.Threads[j]
			t.Comments = slices.Clone(t.Comments)
			t.Origin, t.Status, t.Outgoing = origin, core.ThreadOpen, false // posted: the author's answers are the agent's to read
			for i := range t.Comments {
				if t.Comments[i].Author == core.AuthorYou {
					t.Comments[i].Delivered = true // what was posted is no request of the agent's
				}
			}
		case fresh:
			if len(m.msgs) == 0 {
				continue
			}
			t = core.Thread{ID: core.ThreadID(hostcore.NewID(8)), Origin: origin, Status: core.ThreadOpen, Anchor: m.anchor, Created: now}
		default:
			t = se.Threads[i]
			t.Comments = slices.Clone(t.Comments)
			g := *t.Hosted
			t.Hosted = &g
		}
		before, _ := json.Marshal(t)
		was := t.Hosted != nil && t.Hosted.Resolved
		ref.Resolved = was
		if m.res != nil {
			ref.Resolved = *m.res
		}
		t.Hosted = &ref
		mergeMsgs(&t, m.msgs)
		switch {
		case ref.Resolved && (fresh || !was) && t.Open():
			t.Status, t.Busy = core.ThreadResolved, false
		case !ref.Resolved && was && t.Status == core.ThreadResolved:
			t.Status = core.ThreadOpen
		}
		if after, _ := json.Marshal(t); string(after) == string(before) {
			continue
		}
		t.Updated = now
		if err := x.st.Emit(core.ThreadUpdated{Session: id, Thread: t}); err == nil && t.Open() && len(t.Undelivered()) > 0 {
			kick = true
		}
	}
	for _, t := range se.Threads {
		g := t.Hosted
		if g == nil || g.Host != host || g.Repo != repo || g.PR != pr || g.Root == 0 && g.Key == "" || t.Status == core.ThreadDismissed {
			continue
		}
		if !seen[fmt.Sprint(g.Root, "/", g.Key)] {
			t.Status, t.Busy, t.Updated = core.ThreadDismissed, false, now
			_ = x.st.Emit(core.ThreadUpdated{Session: id, Thread: t})
		}
	}
	return kick
}

// mergeMsgs brings a thread's comments in line with its host's: new ones added, edited ones changed (and given to the
// agent again), deleted ones taken out. A comment of Oriel's (a forwarded draft) is matched by its id there, so it
// isn't added again.
func mergeMsgs(t *core.Thread, msgs []mirrorMsg) {
	is := func(c core.Comment, m mirrorMsg) bool {
		return m.num != 0 && c.GitHubID == m.num || m.key != "" && c.HostID == m.key
	}
	for _, m := range msgs {
		i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return is(c, m) })
		if i < 0 {
			t.Comments = append(t.Comments, core.Comment{ID: hostcore.NewID(6), Author: core.AuthorHost, Name: m.user, Body: m.body, Time: m.at.UTC(),
				GitHubID: m.num, HostID: m.key, Redacted: !m.trusted, Delivered: m.own, Mine: m.own})
			continue
		}
		c := &t.Comments[i]
		c.Mine = c.Mine || m.own
		if c.Author == core.AuthorHost && c.Body != m.body {
			c.Body, c.Edited, c.Delivered = m.body, true, false
			c.Redacted = !m.trusted
		}
	}
	t.Comments = slices.DeleteFunc(t.Comments, func(c core.Comment) bool {
		return c.Author == core.AuthorHost && (c.GitHubID != 0 || c.HostID != "") &&
			!slices.ContainsFunc(msgs, func(m mirrorMsg) bool { return is(c, m) })
	})
}

// MirrorReviewReq brings one PR's review from a code host other than GitHub into a session: Threads as the app read
// them through the host's plugin. A thread with no Key is the PR's conversation.
type MirrorReviewReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Host    string         `json:"host"` // the code host's name
	Repo    string         `json:"repo"`
	PR      int            `json:"pr"`
	URL     string         `json:"url,omitempty"`
	Threads []ReviewThread `json:"threads"`
}

// ReviewThread is a review thread as its code host has it.
type ReviewThread struct {
	Key      string          `json:"key,omitempty"`
	Path     string          `json:"path,omitempty"`
	Line     int             `json:"line,omitempty"`
	Old      bool            `json:"old,omitempty"` // on the old side of the diff
	Outdated bool            `json:"outdated,omitempty"`
	Resolved *bool           `json:"resolved,omitempty"`
	Reviewer string          `json:"reviewer,omitempty"`
	State    string          `json:"state,omitempty"`
	URL      string          `json:"url,omitempty"`
	Comments []ReviewComment `json:"comments"`
}

// ReviewComment is one comment of a ReviewThread; Trusted says the agent may read it.
type ReviewComment struct {
	Key     string    `json:"key"`
	User    string    `json:"user"`
	Body    string    `json:"body"`
	At      time.Time `json:"at"`
	Trusted bool      `json:"trusted,omitempty"`
}

// MirrorReview brings a PR's review threads into the session, and hands the agent what's new.
func (x *GitHub) MirrorReview(ctx context.Context, r MirrorReviewReq) (err error) {
	if r.Host == "" {
		return errors.New("which code host's review?")
	}
	se, err := x.st.Session(r.Session)
	if err != nil {
		return err
	}
	x.mirror.mu.Lock()
	var threads []mirrored
	for _, rt := range r.Threads {
		m := mirrored{ref: core.HostThread{Project: r.Project, Repo: r.Repo, PR: r.PR, Key: rt.Key, Reviewer: rt.Reviewer, State: rt.State,
			URL: cmp.Or(rt.URL, r.URL), Outdated: rt.Outdated}, res: rt.Resolved}
		if rt.Key != "" && rt.Path != "" {
			a := &core.Anchor{Project: r.Project, File: rt.Path, Line: max(rt.Line, 1)}
			if rt.Old {
				a.Side = "old"
			} else {
				a.Text = hostcore.AnchorText(se, a)
			}
			m.anchor = a
		}
		for _, c := range rt.Comments {
			m.msgs = append(m.msgs, mirrorMsg{key: c.Key, user: c.User, body: c.Body, at: c.At, trusted: c.Trusted})
		}
		threads = append(threads, m)
	}
	kick, err := x.applyMirror(r.Session, r.Host, r.Repo, r.PR, threads)
	x.mirror.mu.Unlock()
	if err != nil {
		return err
	}
	if kick && se.Agent.Provider != "" && !se.Archived {
		x.deps.Wake(r.Session)
	}
	hostcore.Restatus(x.st, r.Session)
	return nil
}

// ForwardedReq marks a draft reply sent: posted on its thread's code host as HostID.
type ForwardedReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
	HostID  string         `json:"hostId"`
}

// DraftOf is a thread's draft reply to forward: its body, refusing one that isn't a draft or has gone already.
func (x *GitHub) DraftOf(r ForwardReq) (core.Thread, string, error) {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return core.Thread{}, "", err
	}
	t, err := hostcore.FindThread(se, string(r.Thread))
	if err != nil {
		return core.Thread{}, "", err
	}
	if t.Hosted == nil {
		return t, "", errors.New("this thread isn't from a code host")
	}
	i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return c.ID == r.Comment })
	switch {
	case i < 0:
		return t, "", fmt.Errorf("no comment %s in the thread", r.Comment)
	case t.Comments[i].GitHubID != 0 || t.Comments[i].HostID != "":
		return t, "", errors.New("this reply is already posted")
	case !t.Comments[i].Draft:
		return t, "", errors.New("only a draft reply can be forwarded")
	}
	body := strings.TrimSpace(t.Comments[i].Body)
	if body == "" {
		return t, "", errors.New("the draft is empty")
	}
	return t, body, nil
}

// Forwarded marks a draft reply sent, once its code host has it. A mirror can meanwhile take its echo for a new
// comment: ForwardHost posts and marks with mirroring held off.
func (x *GitHub) Forwarded(r ForwardedReq) (_ core.Comment, err error) {
	x.mirror.mu.Lock()
	defer x.mirror.mu.Unlock()
	return x.forwarded(r.Session, r.Thread, r.Comment, 0, r.HostID)
}

func (x *GitHub) forwarded(id core.SessionID, thread core.ThreadID, comment string, num int64, key string) (core.Comment, error) {
	var sent core.Comment
	_, err := hostcore.MutateThread(x.st, x.env.Now(), id, thread, func(t *core.Thread) error {
		for i := range t.Comments {
			if t.Comments[i].ID == comment {
				t.Comments[i].Draft, t.Comments[i].GitHubID, t.Comments[i].HostID, t.Comments[i].Sent = false, num, key, x.env.Now().UTC()
				sent = t.Comments[i]
			}
		}
		return nil
	})
	hostcore.Restatus(x.st, id)
	return sent, err
}
