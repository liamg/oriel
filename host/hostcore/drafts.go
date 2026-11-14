package hostcore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// Draft is a commit message or PR drafted for a project: by the agent (its provider's name) or "you". Stale says
// the code has moved on since it was written.
type Draft struct {
	Title string    `json:"title,omitempty"`
	Body  string    `json:"body,omitempty"`
	By    string    `json:"by,omitempty"`
	Saved time.Time `json:"saved,omitzero"`
	Stale bool      `json:"stale,omitempty"`
	Code  string    `json:"-"` // the code's fingerprint when it was written (DraftCode)
}

// DraftByYou marks a draft the user wrote: the agent never overwrites it.
const DraftByYou = "you"

// DraftNoun is what each kind of draft is called.
var DraftNoun = map[string]string{"commit": "commit message", "pr": "PR draft"}

// Empty says nothing was drafted.
func (d Draft) Empty() bool { return strings.TrimSpace(d.Title+d.Body) == "" }

// DraftCode is the fingerprint a draft is written against: the uncommitted code for a commit message, the branch's
// whole change from its base for a PR.
func DraftCode(ctx context.Context, repo gitx.Repo, base, kind string) string {
	since := "HEAD"
	if kind == "pr" && base != "" {
		if mb := repo.ForkPoint(ctx, base); mb != "" {
			since = mb
		}
	}
	code, _ := repo.Fingerprint(ctx, since)
	return code
}

// draftPath is where a project's commit message or PR draft is kept: "# title\n\nbody" for a PR.
func (e Env) draftPath(id core.SessionID, project, kind string) string {
	return filepath.Join(e.SessionDir(id), "drafts", Sanitise(project)+"."+kind+".md")
}

// ReadDraft is a project's draft and who wrote it.
func (e Env) ReadDraft(id core.SessionID, project, kind string) Draft {
	path := e.draftPath(id, project, kind)
	data, err := os.ReadFile(path)
	if err != nil {
		return Draft{}
	}
	d := Draft{Body: strings.TrimSpace(string(data))}
	if fi, err := os.Stat(path); err == nil {
		d.Saved = fi.ModTime()
	}
	if kind == "pr" {
		first, rest, _ := strings.Cut(string(data), "\n")
		d.Title, d.Body = strings.TrimSpace(strings.TrimPrefix(first, "#")), strings.TrimSpace(rest)
	}
	if by, err := os.ReadFile(strings.TrimSuffix(path, ".md") + ".by"); err == nil {
		d.By = strings.TrimSpace(string(by))
	}
	if code, err := os.ReadFile(strings.TrimSuffix(path, ".md") + ".code"); err == nil {
		d.Code = strings.TrimSpace(string(code))
	}
	return d
}

// WriteDraft keeps a project's draft, as by wrote it against the code fingerprinted code.
func (e Env) WriteDraft(id core.SessionID, project, kind, title, body, by, code string) error {
	path := e.draftPath(id, project, kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	text := body
	if kind == "pr" {
		text = "# " + title + "\n\n" + body
	}
	if strings.TrimSpace(title+body) == "" {
		return errors.New("the draft is empty")
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(strings.TrimSuffix(path, ".md")+".code", []byte(code), 0o600); err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(path, ".md")+".by", []byte(by), 0o600)
}

// DropDraft forgets a project's draft.
func (e Env) DropDraft(id core.SessionID, project, kind string) {
	path := e.draftPath(id, project, kind)
	_ = os.Remove(path)
	_ = os.Remove(strings.TrimSuffix(path, ".md") + ".by")
	_ = os.Remove(strings.TrimSuffix(path, ".md") + ".code")
}
