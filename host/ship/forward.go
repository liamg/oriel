package ship

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// ForwardHost posts a draft reply on a thread mirrored from a code host with post, which returns the host's id for
// it, and marks it sent. A mirror meanwhile waits for the mark, so the reply's echo is recognised.
func (x *Ships) ForwardHost(ctx context.Context, r ForwardReq, post func(ctx context.Context, t core.Thread, body string) (string, error)) (_ core.Comment, err error) {
	defer x.env.Recover("ForwardHost", &err)
	return x.forward(r, func(t core.Thread, body string) (string, error) { return post(ctx, t, body) })
}

// forward posts a draft with post and marks it sent, with mirroring held off between the two.
func (x *Ships) forward(r ForwardReq, post func(t core.Thread, body string) (key string, err error)) (core.Comment, error) {
	x.mirror.mu.Lock()
	defer x.mirror.mu.Unlock()
	t, body, err := x.DraftOf(r)
	if err != nil {
		return core.Comment{}, err
	}
	key, err := post(t, body)
	if err != nil {
		return core.Comment{}, err
	}
	return x.forwarded(r.Session, t.ID, r.Comment, key)
}

// EditDraft rewrites a draft reply before it's forwarded.
func (x *Ships) EditDraft(r DraftEditReq) (err error) {
	defer x.env.Recover("EditDraft", &err)
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

type ResolveReq = hostapi.ResolveReq

// MarkHostResolved records that a mirrored thread is resolved, or open, on its code host: once the user's resolve has
// reached it.
func (x *Ships) MarkHostResolved(r ResolveReq) (err error) {
	defer x.env.Recover("MarkHostResolved", &err)
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
