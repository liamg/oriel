package ship

import (
	"context"
	"slices"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// commitForm is a project's commit box.
type commitForm struct {
	message string
	edited  bool            // typed into, so the draft no longer fills it
	skip    map[string]bool // files left out
	veto    string          // why a commit gate or a ShipCommit hook stopped the last try
	held    []registry.GateCheck
	save    autosave
}

// renameForm is the branch's new name, typed into the Git panel.
type renameForm struct{ name string }

// prForm is a project's Raise PR tab.
type prForm struct {
	title, body string
	edited      bool
	save        autosave
	tab         int // write or preview
	reviewers   []string
	removed     []string // owners taken off
	adding      string
	veto        string
	editing     bool // a raised PR's form is up, in place of its overview
}

// draftSaveAfter is how long typing in a draft box rests before what's typed is kept in the session.
const draftSaveAfter = time.Second

// autosave keeps what's typed into a draft box as the user's draft: at once on the first key, so the
// agent is neither asked for it nor overwrites it, then each time the typing rests.
type autosave struct {
	pending bool
	at      time.Time
}

// keepTyped is called each frame a draft box is drawn: changed says it was typed into, first that it hadn't been.
func (x *Ship) keepTyped(c *ui.Ctx, a *autosave, changed, first bool, req host.DraftReq) {
	now := c.Now()
	if changed {
		a.pending, a.at = true, now
	}
	if !a.pending {
		return
	}
	if !first && now.Sub(a.at) < draftSaveAfter {
		c.WakeAt(a.at.Add(draftSaveAfter))
		return
	}
	a.pending = false
	cl, err := x.client(req.Session)
	if err != nil {
		return
	}
	x.saving.Add(1)
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		defer x.saving.Done()
		ctx, cancel := context.WithTimeout(x.ctx, 30*time.Second)
		defer cancel()
		if err := cl.ShipDraft(ctx, req); err != nil {
			x.h.Log().Debug("ship", "draft", req.Project, "err", err)
		}
	}()
}

func formKey(id core.SessionID, project string) string { return string(id) + "/" + project }

// commitForm is a project's commit box, made on first use.
func (x *Ship) commitForm(id core.SessionID, project string) *commitForm {
	k := formKey(id, project)
	f := x.commits[k]
	if f == nil {
		f = &commitForm{skip: map[string]bool{}}
		x.commits[k] = f
	}
	return f
}

// message is what a project's commit uses: what's typed, else the draft.
func (f *commitForm) messageFor(s shipRead) string {
	if f.edited {
		return f.message
	}
	return s.CommitDraft.Body
}

// files are the files a project's commit takes: every uncommitted one not left out.
func (f *commitForm) files(s shipRead) []string {
	var out []string
	for _, fl := range s.Files {
		if !f.skip[fl.Path] {
			out = append(out, fl.Path)
		}
	}
	return out
}

// prForm is a project's Raise PR tab, made on first use and filled from the PR, the draft or the template.
func (x *Ship) prForm(id core.SessionID, s shipRead) *prForm {
	k := formKey(id, s.Project)
	f := x.prs[k]
	if f == nil {
		f = &prForm{}
		x.prs[k] = f
	}
	if !f.edited {
		f.title, f.body = prDefaults(s)
	}
	return f
}

// prDefaults are a PR's title and body before anything's typed: the open PR's, else the agent's draft, else the first
// commit's subject, and its body above the repo's template.
func prDefaults(s shipRead) (title, body string) {
	switch {
	case s.PR.Open():
		return s.PR.Title, s.PR.Body
	case !s.PRDraft.Empty():
		return s.PRDraft.Title, s.PRDraft.Body
	}
	first := s.First
	if first.Body == "" {
		return first.Subject, s.Template
	}
	if s.Template == "" {
		return first.Subject, first.Body
	}
	return first.Subject, first.Body + "\n\n" + s.Template
}

// reviewersOf are who a project's PR asks: the CODEOWNERS of its change and those added, less those taken off.
func (f *prForm) reviewersOf(s shipRead) []string {
	var out []string
	for _, r := range append(slices.Clone(s.Owners), f.reviewers...) {
		if !slices.Contains(out, r) && !slices.Contains(f.removed, r) {
			out = append(out, r)
		}
	}
	return out
}
