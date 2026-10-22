package github

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/host/hostcore"
)

// ForwardGitHub posts the agent's draft reply to GitHub as the user, once, and marks it sent.
func (x *GitHub) ForwardGitHub(ctx context.Context, r ForwardReq) (_ core.Comment, err error) {
	return x.forward(r, func(t core.Thread, body string) (int64, string, error) {
		if t.Hosted.Host != "" {
			return 0, "", errors.New("this thread isn't from GitHub")
		}
		_, _, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, t.Hosted.Project)
		if err != nil {
			return 0, "", err
		}
		owner, name, _ := strings.Cut(t.Hosted.Repo, "/")
		ghRepo, c := gh.Repo{Owner: owner, Name: name}, x.github(repo)
		if t.Hosted.Root != 0 {
			rc, err := c.ReplyReview(ctx, ghRepo, t.Hosted.PR, t.Hosted.Root, body)
			if err != nil {
				return 0, "", err
			}
			return rc.ID, "", nil
		}
		ic, err := c.Comment(ctx, ghRepo, t.Hosted.PR, body)
		if err != nil {
			return 0, "", err
		}
		return ic.ID, "", nil
	})
}

// ForwardHost posts a draft reply on a thread mirrored from another code host with post, which returns the host's
// id for it, and marks it sent. A mirror meanwhile waits for the mark, so the reply's echo is recognised.
func (x *GitHub) ForwardHost(ctx context.Context, r ForwardReq, post func(ctx context.Context, t core.Thread, body string) (string, error)) (_ core.Comment, err error) {
	return x.forward(r, func(t core.Thread, body string) (int64, string, error) {
		key, err := post(ctx, t, body)
		return 0, key, err
	})
}

// forward posts a draft with post and marks it sent, with mirroring held off between the two.
func (x *GitHub) forward(r ForwardReq, post func(t core.Thread, body string) (num int64, key string, err error)) (core.Comment, error) {
	x.mirror.mu.Lock()
	defer x.mirror.mu.Unlock()
	t, body, err := x.DraftOf(r)
	if err != nil {
		return core.Comment{}, err
	}
	num, key, err := post(t, body)
	if err != nil {
		return core.Comment{}, err
	}
	return x.forwarded(r.Session, t.ID, r.Comment, num, key)
}

// EditDraft rewrites a draft reply before it's forwarded.
func (x *GitHub) EditDraft(r DraftEditReq) (err error) {
	body := strings.TrimSpace(r.Body)
	if body == "" {
		return errors.New("the draft is empty")
	}
	_, err = hostcore.MutateThread(x.st, x.env.Now(), r.Session, r.Thread, func(t *core.Thread) error {
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

// ResolveReq resolves a thread mirrored from GitHub there too, or reopens it, as the user did in Oriel.
type ResolveReq struct {
	Session  core.SessionID `json:"session"`
	Thread   core.ThreadID  `json:"thread"`
	Resolved bool           `json:"resolved"`
}

// ResolveGitHub resolves or reopens a review thread on GitHub, and records that it is.
func (x *GitHub) ResolveGitHub(ctx context.Context, r ResolveReq) (err error) {
	se, err := x.st.Session(r.Session)
	if err != nil {
		return err
	}
	t, ok := se.Thread(r.Thread)
	if !ok || t.Hosted == nil || t.Hosted.Host != "" || t.Hosted.Root == 0 {
		return fmt.Errorf("thread %s isn't a GitHub review thread", r.Thread)
	}
	_, _, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, t.Hosted.Project)
	if err != nil {
		return err
	}
	owner, name, _ := strings.Cut(t.Hosted.Repo, "/")
	if err := x.github(repo).ResolveThread(ctx, gh.Repo{Owner: owner, Name: name}, t.Hosted.PR, t.Hosted.Root, r.Resolved); err != nil {
		return err
	}
	return x.MarkHostResolved(r)
}

// MarkHostResolved records that a mirrored thread is resolved, or open, on its code host: once the user's resolve has
// reached it.
func (x *GitHub) MarkHostResolved(r ResolveReq) (err error) {
	_, err = hostcore.MutateThread(x.st, x.env.Now(), r.Session, r.Thread, func(t *core.Thread) error {
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
