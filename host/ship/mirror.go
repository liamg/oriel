package ship

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

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// Review mirroring for any code host: a PR's review threads and conversation, as the app's review mirror reads them
// from the host, become threads with origin codehost, which reach the agent like any comment. On them the agent only
// drafts; the user forwards a draft, and its code host posts it as the user. A forwarded comment carries its id
// there, so it never comes back as a new comment (echo reconciliation); edits, deletions and resolutions there update
// the mirror.

// mirrorState serialises applying a mirror with a forward's post and mark, so a forward's echo is always recognised.
type mirrorState struct {
	mu sync.Mutex
}

// mirrorMsg is one comment of a mirrored thread; key is its id on the host.
type mirrorMsg struct {
	key     string
	user    string
	body    string
	at      time.Time
	trusted bool // the agent may read its text
	own     bool // the user's own: not given to the agent as theirs to answer
	aside   bool // another's, for the agent to read but not to answer: in a review, see asides
}

// mirrored is a thread as its code host has it.
type mirrored struct {
	ref    core.HostThread
	anchor *core.Anchor
	msgs   []mirrorMsg
	res    *bool         // resolved there; nil when not known
	into   core.ThreadID // the session's thread it was posted from, which it joins
}

// applyMirror brings a PR's threads on host in line with threads, and dismisses its mirrored review
// threads the host no longer has; kick says the agent has something new. It holds the threads' edit lock, so the
// agent's own changes to them meanwhile aren't lost.
func (x *Ships) applyMirror(id core.SessionID, host, repo string, pr int, threads []mirrored) (bool, error) {
	kick := false
	err := x.st.Mutate(id, func(se core.SessionState) error {
		kick = x.mirrorInto(se, host, repo, pr, threads)
		return nil
	})
	return kick, err
}

// mirrorInto is applyMirror on se, under the edit lock.
func (x *Ships) mirrorInto(se core.SessionState, host, repo string, pr int, threads []mirrored) bool {
	id := se.ID
	now := x.env.Now().UTC()
	origin := core.OriginCodeHost
	same := func(g *core.HostThread, ref core.HostThread) bool {
		return g != nil && g.Host == host && g.Repo == repo && g.PR == pr && g.Key == ref.Key
	}
	kick := false
	seen := map[string]bool{}
	for _, m := range threads {
		ref := m.ref
		ref.Host = host
		if ref.Key != "" {
			seen[ref.Key] = true
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
		if g == nil || g.Host != host || g.Repo != repo || g.PR != pr || g.Key == "" || t.Status == core.ThreadDismissed {
			continue
		}
		if !seen[g.Key] {
			t.Status, t.Busy, t.Updated = core.ThreadDismissed, false, now
			_ = x.st.Emit(core.ThreadUpdated{Session: id, Thread: t})
		}
	}
	return kick
}

// asides marks what of a reviewed PR's thread its review's agent reads but needn't answer: in a review thread, a
// first comment nobody but the user has answered, unless the PR's author wrote it; in the PR's conversation (convo),
// everything but the author's. The user's own are never the agent's to answer either (own). Outside a review
// (author ""), the agent answers them all.
func asides(msgs []mirrorMsg, author string, convo bool) {
	if author == "" || len(msgs) == 0 {
		return
	}
	if convo {
		for i := range msgs {
			msgs[i].aside = msgs[i].user != author
		}
		return
	}
	answered := slices.ContainsFunc(msgs[1:], func(m mirrorMsg) bool { return !m.own })
	msgs[0].aside = !answered && msgs[0].user != author
}

// mergeMsgs brings a thread's comments in line with its host's: new ones added, edited ones changed (and given to the
// agent again), deleted ones taken out. A comment of Oriel's (a forwarded draft) is matched by its id there, so it
// isn't added again.
func mergeMsgs(t *core.Thread, msgs []mirrorMsg) {
	is := func(c core.Comment, m mirrorMsg) bool { return m.key != "" && c.HostID == m.key }
	for _, m := range msgs {
		i := slices.IndexFunc(t.Comments, func(c core.Comment) bool { return is(c, m) })
		if i < 0 {
			t.Comments = append(t.Comments, core.Comment{ID: hostcore.NewID(6), Author: core.AuthorHost, Name: m.user, Body: m.body, Time: m.at.UTC(),
				HostID: m.key, Redacted: !m.trusted, Delivered: m.own || m.aside, Mine: m.own})
			continue
		}
		c := &t.Comments[i]
		c.Mine = c.Mine || m.own
		if c.Author == core.AuthorHost && c.Body != m.body {
			c.Body, c.Edited, c.Delivered = m.body, true, m.own || m.aside
			c.Redacted = !m.trusted
		}
	}
	t.Comments = slices.DeleteFunc(t.Comments, func(c core.Comment) bool {
		return c.Author == core.AuthorHost && c.HostID != "" &&
			!slices.ContainsFunc(msgs, func(m mirrorMsg) bool { return is(c, m) })
	})
}

// MirrorReview brings a PR's review threads into the session, and hands the agent what's new.
func (x *Ships) MirrorReview(ctx context.Context, r hostapi.MirrorReviewReq) (err error) {
	defer x.env.Recover("MirrorReview", &err)
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
		review := reviewedPR(se) != ""
		for _, c := range rt.Comments {
			m.msgs = append(m.msgs, mirrorMsg{key: c.Key, user: c.User, body: c.Body, at: c.At, trusted: c.Trusted, own: review && c.Mine})
		}
		if review {
			asides(m.msgs, r.Author, rt.Key == "")
			if rt.Key != "" && rt.Path != "" && len(rt.Comments) > 0 {
				first := rt.Comments[0]
				m.into = sentFrom(se, rt.Path, cmp.Or(first.Written, first.Body))
			}
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

// DraftOf is a thread's draft reply to forward: its body, refusing one that isn't a draft or has gone already.
func (x *Ships) DraftOf(r hostapi.ForwardReq) (_ core.Thread, _ string, err error) {
	defer x.env.Recover("DraftOf", &err)
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
	case t.Comments[i].HostID != "":
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
func (x *Ships) Forwarded(r hostapi.ForwardedReq) (_ core.Comment, err error) {
	defer x.env.Recover("Forwarded", &err)
	x.mirror.mu.Lock()
	defer x.mirror.mu.Unlock()
	return x.forwarded(r.Session, r.Thread, r.Comment, r.HostID)
}

func (x *Ships) forwarded(id core.SessionID, thread core.ThreadID, comment string, key string) (core.Comment, error) {
	var sent core.Comment
	_, err := hostcore.MutateThread(x.st, x.env.Now(), id, thread, func(t *core.Thread) error {
		for i := range t.Comments {
			if t.Comments[i].ID == comment {
				t.Comments[i].Draft, t.Comments[i].HostID, t.Comments[i].Sent = false, key, x.env.Now().UTC()
				sent = t.Comments[i]
			}
		}
		return nil
	})
	hostcore.Restatus(x.st, id)
	return sent, err
}
