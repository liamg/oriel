package ship

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// project is the project a command names, else the open one.
func (x *Ship) project(a registry.Args) (core.SessionState, string, error) {
	se, ok := x.session(a)
	if !ok {
		return se, "", errors.New("no session is in front")
	}
	p := a.String("project")
	if p == "" {
		p = se.OpenProject()
	}
	if !slices.ContainsFunc(se.Projects, func(q core.Project) bool { return q.Name == p }) {
		return se, "", fmt.Errorf("the session has no project %q", p)
	}
	return se, p, nil
}

func (x *Ship) commands() {
	for _, c := range slices.Concat(x.shipCommands(), x.branchCommands(), x.draftCommands()) {
		x.Add(x.h.Command(c))
	}
}

// inFront says whether a session is in front.
func (x *Ship) inFront(registry.Args) bool { _, ok := x.session(nil); return ok }

// onProject runs fn on the project a command names.
func (x *Ship) onProject(fn func(ctx registry.Ctx, se core.SessionState, project string, a registry.Args) error) func(registry.Ctx, registry.Args) error {
	return func(ctx registry.Ctx, a registry.Args) error {
		se, p, err := x.project(a)
		if err != nil {
			return err
		}
		return fn(ctx, se, p, a)
	}
}

// shipCommands open the page and commit, push, sync and raise.
func (x *Ship) shipCommands() []registry.Command {
	inFront, run := x.inFront, x.onProject
	return []registry.Command{
		{ID: "ship.open", Title: "Open the Ship page: every project's way to a merged PR", Available: inFront,
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return x.h.Run(ctx, "view.open", registry.Args{"id": kit.ShipView, "place": "tab", "title": "Ship"})
			}},
		{ID: "ship.refresh", Title: "Read the session's projects again, from their worktrees and GitHub", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				x.refresh(se.ID, true)
				return nil
			}},
		{ID: "ship.commit", Title: "Commit a project's changes: {project}, {message} (its draft by default), {files} (all by default)",
			Available: inFront, Run: run(x.commit)},
		{ID: "ship.commit-all", Title: "Commit every project's changes, each with its message", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				var errs []error
				for _, s := range x.report(se.ID).ships {
					if len(s.Files) > 0 && !s.ReadOnly {
						errs = append(errs, x.commit(ctx, se, s.Project, registry.Args{}))
					}
				}
				return errors.Join(errs...)
			}},
		{ID: "ship.push", Title: "Push a project's branch to origin: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				return x.step(se.ID, p, "Pushing", func(ctx context.Context, c host.Access) error {
					return c.ShipPush(ctx, host.ShipRef{Session: se.ID, Project: p})
				})
			})},
		{ID: "ship.push-all", Title: "Push every project with an open PR and commits origin hasn't got", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				var errs []error
				for _, s := range x.report(se.ID).ships {
					if !s.ReadOnly && s.PR.Open() && len(s.Unpushed) > 0 {
						p := s.Project
						errs = append(errs, x.step(se.ID, p, "Pushing", func(ctx context.Context, c host.Access) error {
							return c.ShipPush(ctx, host.ShipRef{Session: se.ID, Project: p})
						}))
					}
				}
				return errors.Join(errs...)
			}},
		{ID: "ship.sync", Title: "Rebase a project's branch onto its base, fetched: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				return x.sync(se, p)
			})},
		{ID: "ship.sync-all", Title: "Rebase every project that's behind its base", Available: inFront,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				var errs []error
				for _, s := range x.report(se.ID).ships {
					if !s.ReadOnly && s.Remote {
						errs = append(errs, x.sync(se, s.Project))
					}
				}
				return errors.Join(errs...)
			}},
		{ID: "ship.raise", Title: "Push a project and raise its PR, or update the open one: {project}, {title}, {body}, {draft}, {reviewers}",
			Available: inFront, Run: run(x.raise)},
		{ID: "ship.raise-all", Title: "Push and raise every project's PR, in merge order", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				return x.raiseAll(ctx, se)
			}},
	}
}

// branchCommands rename, merge and move on a project's branch and its PR.
func (x *Ship) branchCommands() []registry.Command {
	inFront, run := x.inFront, x.onProject
	return []registry.Command{
		{ID: "ship.rename", Title: "Rename a project's branch: {project}, {name}; with no name, ask for one", Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				name := strings.TrimSpace(a.String("name"))
				if name == "" {
					x.renaming[formKey(se.ID, p)] = &renameForm{}
					return nil
				}
				delete(x.renaming, formKey(se.ID, p))
				s, _ := x.ship(se.ID, p)
				return x.step(se.ID, p, "Renaming", func(ctx context.Context, c host.Access) error {
					if err := x.renameGuard(ctx, se, s); err != nil {
						return err
					}
					return c.ShipRename(ctx, host.RenameReq{Session: se.ID, Project: p, Name: name})
				})
			})},
		{ID: "ship.merge-default", Title: "Merge a project's branch into its repo's default branch here, without a PR and not pushed, after asking: {project}; {confirm} merges",
			Available: inFront, Run: run(x.mergeDefault)},
		{ID: "ship.next-pr", Title: "Move a project on from its merged or closed PR to a fresh branch from its updated base: {project}",
			Available: inFront, Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				return x.nextPR(se, p, nil)
			})},
		{ID: "ship.ready", Title: "Mark a project's draft PR ready for review: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				s, _ := x.ship(se.ID, p)
				return x.step(se.ID, p, "Marking ready", func(ctx context.Context, _ host.Access) error {
					return x.markReady(ctx, se, s)
				})
			})},
		{ID: "ship.pr.open", Title: "Open a project's PR tab: Raise PR, or the raised PR's overview; {edit} true for its form, false back to the overview: {project}",
			Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				if _, ok := a["edit"]; ok {
					if s, read := x.ship(se.ID, p); read && s.PR != nil {
						x.editPR(se.ID, p, a.Bool("edit"))
					}
				}
				return x.h.Run(ctx, "view.open", registry.Args{"id": kit.PRView, "instance": p, "place": "tab"})
			})},
		{ID: "ship.fix-ci", Title: "Write a request asking the agent to fix a project's failing CI: {project}", Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				s, _ := x.ship(se.ID, p)
				t, ok := ciTrouble(s)
				if !ok {
					return fmt.Errorf("%s has no failing checks", p)
				}
				return x.h.Run(ctx, registry.FixCommand, t.Args())
			})},
	}
}

// draftCommands redraft and save drafts, and open or copy a PR's link.
func (x *Ship) draftCommands() []registry.Command {
	inFront, run := x.inFront, x.onProject
	return []registry.Command{
		{ID: "ship.redraft", Title: "Ask the agent to draft a project's {kind} again: commit or pr", Available: inFront,
			Run: run(func(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				kind := cmp.Or(a.String("kind"), "commit")
				id := "drafts.commit.again"
				if kind == "pr" {
					id = "drafts.pr.again"
				}
				text := kit.RenderPrompt(x.ConfigDir, id, map[string]string{"project": p})
				c, err := x.client(se.ID)
				if err != nil {
					return err
				}
				if kind == "commit" {
					f := x.commitForm(se.ID, p)
					f.edited, f.save = false, autosave{}
				} else if s, ok := x.ship(se.ID, p); ok {
					f := x.prForm(se.ID, s)
					f.edited, f.save = false, autosave{}
				}
				x.saving.Wait()
				if err := c.ShipDraft(ctx, host.DraftReq{Session: se.ID, Project: p, Kind: kind, Drop: true}); err != nil {
					return err
				}
				return c.Send(host.SendReq{Session: se.ID, Text: text})
			})},
		{ID: "ship.draft.save", Title: "Save a project's draft as yours, which the agent leaves: {project}, {kind} pr (the Raise PR tab's, by default) or commit (the commit box's), {title}, {body}",
			Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				kind := cmp.Or(a.String("kind"), "pr")
				s, _ := x.ship(se.ID, p)
				req := host.DraftReq{Session: se.ID, Project: p, Kind: kind}
				switch kind {
				case "pr":
					f := x.prForm(se.ID, s)
					req.Title, req.Body = cmp.Or(a.String("title"), f.title), cmp.Or(a.String("body"), f.body)
				case "commit":
					req.Body = cmp.Or(a.String("body"), x.commitForm(se.ID, p).messageFor(s))
				default:
					return fmt.Errorf("a draft is a commit or a pr, not %q", kind)
				}
				return x.step(se.ID, p, "Saving", func(ctx context.Context, c host.Access) error { return c.ShipDraft(ctx, req) })
			})},
		{ID: "ship.browse", Title: "Open a project's PR, or a past one by {number}, in the browser: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, a registry.Args) error {
				if n := a.Int("number"); n > 0 {
					for _, pr := range se.Past {
						if pr.Project == p && pr.Number == n && pr.URL != "" {
							return kit.OpenURL(x.h, pr.URL)
						}
					}
					return fmt.Errorf("%s has no past PR #%d", p, n)
				}
				s, _ := x.ship(se.ID, p)
				if s.PR == nil || s.PR.URL == "" {
					return fmt.Errorf("%s has no PR yet", p)
				}
				return kit.OpenURL(x.h, s.PR.URL)
			})},
		{ID: "ship.copy-link", Title: "Copy the link to a project's PR to the clipboard: {project}", Available: inFront,
			Run: run(func(_ registry.Ctx, se core.SessionState, p string, _ registry.Args) error {
				s, _ := x.ship(se.ID, p)
				if s.PR == nil || s.PR.URL == "" {
					return fmt.Errorf("%s has no PR yet", p)
				}
				kit.CopyText(x.h, s.PR.URL)
				x.h.Notify(core.Notice{ID: "ship.copied", Severity: core.SevInfo, Key: true, Title: p, Text: "Copied " + s.PR.URL})
				return nil
			})},
	}
}

// commit fires ShipCommit, then commits. A veto is kept in the commit box.
func (x *Ship) commit(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
	s, _ := x.ship(se.ID, p)
	f := x.commitForm(se.ID, p)
	ev := &ShipCommit{Session: string(se.ID), Project: p, Message: cmp.Or(a.String("message"), f.messageFor(s))}
	if fs := paths(a["files"]); len(fs) > 0 {
		ev.Files = fs
	} else if len(f.skip) > 0 {
		ev.Files = f.files(s)
		if len(ev.Files) == 0 {
			return fmt.Errorf("no files of %s are ticked to commit", p)
		}
	}
	if strings.TrimSpace(ev.Message) == "" {
		return fmt.Errorf("%s's commit needs a message", p)
	}
	if v, _ := x.h.Fire(ctx, "ShipCommit", p, ev); v.Veto {
		f.veto = v.Reason
		return fmt.Errorf("%w: %s", kit.ErrStopped, v.Reason)
	}
	f.veto = ""
	return x.step(se.ID, p, "Committing", func(cx context.Context, c host.Access) error {
		x.saving.Wait()
		_, err := c.ShipCommit(cx, host.ShipCommitReq{Session: se.ID, Project: p, Message: ev.Message, Files: ev.Files})
		return err
	}, func() { *f = commitForm{skip: map[string]bool{}} })
}

func (x *Ship) sync(se core.SessionState, p string) error {
	return x.step(se.ID, p, "Rebasing", func(ctx context.Context, c host.Access) error {
		return c.ShipSync(ctx, host.ShipRef{Session: se.ID, Project: p})
	})
}

// raiseReq is the PR a project's raise sends: the args', else the tab's, else its defaults, through ShipRaise.
func (x *Ship) raiseReq(ctx registry.Ctx, se core.SessionState, p string, a registry.Args, all bool) (prReq, error) {
	s, ok := x.ship(se.ID, p)
	if !ok {
		return prReq{}, fmt.Errorf("%s hasn't been read yet", p)
	}
	f := x.prForm(se.ID, s)
	ev := &ShipRaise{Session: string(se.ID), Project: p, Title: cmp.Or(a.String("title"), f.title), Body: cmp.Or(a.String("body"), f.body),
		Draft: a.Bool("draft"), Reviewers: f.reviewersOf(s), Edit: s.PR.Open(), All: all}
	if rs := paths(a["reviewers"]); len(rs) > 0 {
		ev.Reviewers = rs
	}
	if strings.TrimSpace(ev.Title) == "" {
		return prReq{}, fmt.Errorf("%s's %s needs a title", p, s.words().Short)
	}
	if !s.PR.Open() && s.BaseAhead > 0 {
		return prReq{}, fmt.Errorf("%s: push %s first: it has %s origin hasn't, which the %s would show again", p, s.Base,
			kit.Counted(s.BaseAhead, "commit"), s.words().Short)
	}
	if v, _ := x.h.Fire(ctx, "ShipRaise", p, ev); v.Veto {
		f.veto = v.Reason
		return prReq{}, fmt.Errorf("%w: %s", kit.ErrStopped, v.Reason)
	}
	f.veto = ""
	return prReq{Project: p, Title: ev.Title, Body: ev.Body, Draft: ev.Draft, Reviewers: ev.Reviewers}, nil
}

// raise pushes what's unpushed, then raises the PR or edits it.
func (x *Ship) raise(ctx registry.Ctx, se core.SessionState, p string, a registry.Args) error {
	req, err := x.raiseReq(ctx, se, p, a, false)
	if err != nil {
		return err
	}
	s, _ := x.ship(se.ID, p)
	if x.setupFirst(ctx, s) {
		return nil
	}
	label := "Raising"
	if s.PR.Open() {
		label = "Updating"
	}
	return x.step(se.ID, p, label, func(cx context.Context, c host.Access) error {
		if len(s.Unpushed) > 0 || !s.Pushed {
			if err := c.ShipPush(cx, host.ShipRef{Session: se.ID, Project: p}); err != nil {
				return err
			}
		}
		if err := x.raisePR(cx, se, s, req); err != nil {
			return err
		}
		x.h.Do(func() { x.editPR(se.ID, p, false) })
		return nil
	})
}

// raiseAll raises each project with commits, one after another in merge order, so each PR links the ones before it.
func (x *Ship) raiseAll(ctx registry.Ctx, se core.SessionState) error {
	var reqs []prReq
	for _, s := range x.report(se.ID).ships {
		if s.ReadOnly || s.Code == nil || s.Commits == 0 && s.PR == nil {
			continue
		}
		req, err := x.raiseReq(ctx, se, s.Project, registry.Args{}, true)
		if err != nil {
			return err
		}
		reqs = append(reqs, req)
	}
	if len(reqs) == 0 {
		return errors.New("no project has commits to raise")
	}
	if _, err := x.client(se.ID); err != nil {
		return err
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		for _, req := range reqs {
			done := make(chan struct{})
			s, _ := x.ship(se.ID, req.Project)
			err := x.step(se.ID, req.Project, "Raising", func(cx context.Context, c host.Access) error {
				defer close(done)
				if err := c.ShipPush(cx, host.ShipRef{Session: se.ID, Project: req.Project}); err != nil {
					return err
				}
				return x.raisePR(cx, se, s, req)
			})
			if err != nil {
				x.h.Notify(core.Notice{ID: "ship." + req.Project, Severity: core.SevError, Key: true, Title: req.Project, Text: err.Error()})
				continue
			}
			<-done
		}
	}()
	return nil
}
