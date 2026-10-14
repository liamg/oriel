package worker

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
)

// Commit and PR drafts when the agent goes idle (AI-10): the pack tells the agent to fill them before it ends a turn,
// and when a turn ends with nothing queued and a draft missing or older than the code, the host asks it once for
// that code.

var draftNoun = map[string]string{"commit": "commit message", "pr": "PR draft"}

// draftCode is the fingerprint a draft is written against: the uncommitted code for a commit message, the branch's
// whole change from its base for a PR.
func draftCode(ctx context.Context, repo gitx.Repo, base, kind string) string {
	since := "HEAD"
	if kind == "pr" && base != "" {
		if mb := repo.MergeBase(ctx, base); mb != "" {
			since = mb
		}
	}
	code, _ := repo.Fingerprint(ctx, since)
	return code
}

// dueDraft is a draft a project lacks, for the code fingerprinted code.
type dueDraft struct {
	project, kind, code string
}

// draftsDue are the drafts a session's projects lack: a commit message for uncommitted work, a PR draft for a branch
// with commits of its own or uncommitted work and no open PR; each missing or written for other code, and never
// one the user wrote.
func (w *Worker) draftsDue(ctx context.Context, id core.SessionID) []dueDraft {
	se, err := w.session(id)
	if err != nil {
		return nil
	}
	var out []dueDraft
	for _, p := range se.Projects {
		if p.ReadOnly || p.Detached || p.Dir == "" {
			continue
		}
		env, err := sandbox.GitEnv(p.Dir)
		if err != nil {
			continue
		}
		repo := gitx.Repo{Dir: p.Dir, Env: env}
		if _, err := repo.Head(ctx); err != nil {
			continue
		}
		dirty := repo.Dirty(ctx)
		base := repo.FlightBase(ctx, p.Base)
		ahead := false
		if base != "" {
			if n, err := repo.Run(ctx, "rev-list", "--count", base+"..HEAD"); err == nil {
				c, _ := strconv.Atoi(strings.TrimSpace(n))
				ahead = c > 0
			}
		}
		want := map[string]bool{"commit": dirty, "pr": (dirty || ahead) && !w.prOpen(id, p.Name, p.Branch)}
		for _, kind := range []string{"commit", "pr"} {
			if !want[kind] {
				continue
			}
			d := w.readDraft(id, p.Name, kind)
			if d.By == DraftByYou {
				continue
			}
			code := draftCode(ctx, repo, base, kind)
			if d.Empty() || d.code != code {
				out = append(out, dueDraft{p.Name, kind, code})
			}
		}
	}
	return out
}

// prOpen says the last GitHub reading of a branch found its PR open, whose page shows the PR rather than a draft.
func (w *Worker) prOpen(id core.SessionID, project, branch string) bool {
	st := w.shipMu()
	st.mu.Lock()
	defer st.mu.Unlock()
	g, ok := st.github[shipKey(id, project)+"/"+branch]
	return ok && g.pr.Open()
}

// idle runs when a turn ends with nothing for the agent: if a draft is due for code it hasn't been asked about, it's
// asked, as a notice rather than a thread.
func (h *host) idle() {
	h.mu.Lock()
	ok := h.drafts && h.sess != nil && !h.working && !h.starting
	h.mu.Unlock()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(h.ctx, 30*time.Second)
	due := h.w.draftsDue(ctx, h.id)
	cancel()
	h.mu.Lock()
	var ask []dueDraft
	for _, d := range due {
		key := d.project + "/" + d.kind
		if h.nudged[key] == d.code {
			continue
		}
		h.nudged[key] = d.code
		ask = append(ask, d)
	}
	h.mu.Unlock()
	if len(ask) > 0 {
		h.note(draftsNotice(len(h.snapshot().Projects) > 1, ask))
	}
}

// draftsNotice asks the agent for the drafts due.
func draftsNotice(projects bool, due []dueDraft) string {
	var parts []string
	for _, d := range due {
		tool := map[string]string{"commit": "commit_draft", "pr": "pr_draft"}[d.kind]
		part := fmt.Sprintf("a %s (mcp__oriel__%s", draftNoun[d.kind], tool)
		if projects {
			part += fmt.Sprintf(", project %q", d.project)
		}
		parts = append(parts, part+")")
	}
	return "The work has gone quiet and the Ship page's drafts are behind the code. Fill them as your skill says " +
		"(When the work goes quiet): " + strings.Join(parts, "; ") + ". No thread or reply, and don't commit or raise the PR."
}
