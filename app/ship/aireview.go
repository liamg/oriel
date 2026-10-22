package ship

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent/packs"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// AIReviewView is the AI review's column at the diff's right: the findings of a review by another model.
const AIReviewView = kit.DiffRight + "50-ai-review"

var (
	tFindBg       = theme.Declare("review.finding.bg", "bg.raised")
	tFindLine     = theme.Declare("review.finding.line", "line.strong")
	tFindWhere    = theme.Declare("review.finding.where", "cyan")
	tFindReviewer = theme.Declare("review.reviewer", "cyan")
)

// AIReview is the cross-model AI review: its commands, options and the diff's column.
// Reviews run on the session host (host.Host.AIReview); findings are session state.
type AIReview struct {
	kit.Disposer
	ConfigDir string // the user's config directory, for their prompts
	h         plugin.Host

	filter    string // a severity the cards are narrowed to, or ""
	dismissed bool   // dismissed findings are shown too
	busy      sync.WaitGroup
	asked     map[core.SessionID]bool // a review asked for and not answered yet
}

func (x *AIReview) Manifest() plugin.Manifest { return plugin.Manifest{ID: "ai-review"} }

func (x *AIReview) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.Add(h.Option(registry.OptionSpec{Name: "review.ai.provider", Type: registry.String, Default: "", Scopes: registry.ScopeUser,
		Help: "The provider AI reviews run on: claude, codex; empty for one other than the session agent's (the author's), else the author's on another model."}))
	x.Add(h.Option(registry.OptionSpec{Name: "review.ai.model", Type: registry.String, Default: "", Scopes: registry.ScopeUser,
		Help: "The model AI reviews run on; empty for the reviewer's default."}))
	x.Add(h.Contribute(registry.ShipGate{Name: "ai-review", Title: "AI review", Order: 60, Check: x.shipGate, Source: "builtin:ai-review"}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.View(AIReviewView, aiReviewView{x}))
	return nil
}

func (x *AIReview) commands() []registry.Command {
	has := func(a registry.Args) bool {
		_, err := x.client(a)
		return err == nil
	}
	finding := func(fn func(ctx context.Context, se core.SessionState, f core.Finding, a registry.Args) error) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, a registry.Args) error {
			se, err := x.session(a)
			if err != nil {
				return err
			}
			f, ok := se.Finding(a.String("finding"))
			if !ok {
				return fmt.Errorf("no finding %q", a.String("finding"))
			}
			return fn(ctx, se, f, a)
		}
	}
	return []registry.Command{
		{ID: "review.ai.run", Title: "AI review the change of session {id} (the one in front by default): what changed since the last review, or all of it with {full}; on another model than the author's",
			Available: has, Run: func(_ registry.Ctx, a registry.Args) error { return x.run(a) }},
		{ID: "review.ai.thread", Title: "Turn the AI review's {finding} into a thread on its line, for the session's agent to answer", Available: has,
			Run: finding(func(ctx context.Context, se core.SessionState, f core.Finding, _ registry.Args) error {
				return x.thread(ctx, se, f, false)
			})},
		{ID: "review.ai.fix", Title: "Ask the session's agent to fix the AI review's {finding}, in a thread on its line", Available: has,
			When: core.OwnsChanges,
			Run: finding(func(ctx context.Context, se core.SessionState, f core.Finding, _ registry.Args) error {
				return x.thread(ctx, se, f, true)
			})},
		{ID: "review.ai.dismiss", Title: "Dismiss the AI review's {finding}, or bring it back with {dismissed} false", Available: has,
			Run: finding(func(_ context.Context, se core.SessionState, f core.Finding, a registry.Args) error {
				f.Dismissed = true
				if v, ok := a["dismissed"].(bool); ok {
					f.Dismissed = v
				}
				return x.setFinding(se, f)
			})},
		{ID: "review.ai.add-project", Title: "Add the repo the AI review's {finding} is about to the session", Available: has,
			Run: finding(func(ctx context.Context, se core.SessionState, f core.Finding, _ registry.Args) error {
				if f.Repo == "" {
					return errors.New("the finding names no other repo")
				}
				return x.h.Run(ctx, "session.add-repo", registry.Args{"id": string(se.ID), "repo": f.Repo})
			})},
		{ID: "review.ai.open", Title: "Show the AI review's {finding} at its line in the diff",
			Run: finding(func(ctx context.Context, se core.SessionState, f core.Finding, _ registry.Args) error {
				path := findingPath(se, f)
				if path == "" {
					return errors.New("the finding names no file")
				}
				return x.h.Run(ctx, "diff.open", registry.Args{"path": path, "line": f.Line})
			})},
		{ID: "review.ai.filter", Title: "Show only the AI review's findings of {severity}, or all of them again",
			Run: func(_ registry.Ctx, a registry.Args) error {
				s := a.String("severity")
				if s == x.filter {
					s = ""
				}
				x.filter = s
				return nil
			}},
		{ID: "review.ai.dismissed", Title: "Show or hide the AI review's dismissed findings",
			Run: func(registry.Ctx, registry.Args) error { x.dismissed = !x.dismissed; return nil }},
		{ID: "review.ai.prompt", Title: "Edit the AI review's prompt: your agents/ai-review.md, made from the built-in one the first time",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				path := x.promptPath()
				if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
					p, _ := packs.Get(host.AIReviewPack)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						return err
					}
					if err := os.WriteFile(path, []byte(p.Text), 0o644); err != nil {
						return err
					}
				}
				return x.h.Run(ctx, "editor.open", registry.Args{"path": path})
			}},
	}
}

func (x *AIReview) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

// aiReviewView is the column, shown for a diff in a session.
type aiReviewView struct{ x *AIReview }

func (v aiReviewView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "ai-review", Fn: func(c *ui.Ctx) { v.x.view(c, vc.Instance, vc.Width) }}
}

func (v aiReviewView) Shown(root string) bool {
	_, _, ok := kit.SessionAt(v.x.h.State(), root)
	return ok
}

// promptPath is the user's own AI review prompt, which overrides the built-in pack.
func (x *AIReview) promptPath() string {
	return filepath.Join(x.ConfigDir, "agents", host.AIReviewPack+".md")
}

// session is the session {id}, or the one in front.
func (x *AIReview) session(a registry.Args) (core.SessionState, error) {
	return kit.SessionFrom(x.h, a)
}

func (x *AIReview) client(a registry.Args) (host.Access, error) {
	if _, err := x.session(a); err != nil {
		return nil, err
	}
	return kit.SessionHost(x.h)
}

// run asks the session host for a review, with the user's prompt when they have one.
func (x *AIReview) run(a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	c, err := x.client(a)
	if err != nil {
		return err
	}
	opt := func(name string) string {
		v, _, _ := x.h.Options().Get(name, registry.At{})
		s, _ := v.(string)
		return s
	}
	if x.asked[se.ID] {
		return nil
	}
	req := host.AIReviewReq{Session: se.ID, Provider: opt("review.ai.provider"), Model: opt("review.ai.model"), Full: a.Bool("full")}
	if b, err := os.ReadFile(x.promptPath()); err == nil {
		p, err := packs.FromText(string(b), host.AIReviewPack)
		if err != nil {
			return fmt.Errorf("%s: %w", x.promptPath(), err)
		}
		req.Pack = p.Text
	}
	if x.asked == nil {
		x.asked = map[core.SessionID]bool{}
	}
	x.asked[se.ID] = true
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		_, err := c.AIReview(context.Background(), req)
		x.h.Do(func() { delete(x.asked, se.ID) })
		x.h.Redraw()
		if err != nil {
			x.h.Notify(core.Notice{ID: "review.ai", Severity: core.SevError, Key: true, Text: "AI review: " + err.Error()})
		}
	}()
	return nil
}

func (x *AIReview) setFinding(se core.SessionState, f core.Finding) error {
	c, err := kit.SessionHost(x.h)
	if err != nil {
		return err
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		if err := c.SetFindings(core.FindingsRecorded{Session: se.ID, Findings: []core.Finding{f}}); err != nil {
			x.h.Notify(core.Notice{ID: "review.ai", Severity: core.SevError, Key: true, Text: err.Error()})
		}
	}()
	return nil
}

// thread hands a finding to the session's agent as a thread on its line: to answer, or to fix.
func (x *AIReview) thread(ctx context.Context, se core.SessionState, f core.Finding, fix bool) error {
	if f.Thread != "" {
		return x.h.Run(ctx, "request.open", registry.Args{"thread": string(f.Thread)})
	}
	who := "An AI review"
	if r, ok := reviewOf(se, f); ok {
		who = "An AI review by " + reviewerLabel(r)
	}
	kind := f.Severity
	if f.Category != "" {
		kind += ", " + f.Category
	}
	body := fmt.Sprintf("%s found (%s): **%s**", who, kind, f.Title)
	if f.Body != "" {
		body += "\n\n" + f.Body
	}
	if fix {
		body += "\n\nFix it, then reply here with what you changed."
	} else {
		body += "\n\nIs it right? Answer here before changing anything."
	}
	a := registry.Args{"id": string(se.ID), "title": f.Title, "body": body, "severity": f.Severity, "finding": f.ID}
	if path := findingPath(se, f); path != "" {
		a["file"], a["line"] = path, max(f.Line, 1)
	}
	return x.h.Run(ctx, "request.comment", a)
}

// findingPath is the file a finding is about.
func findingPath(se core.SessionState, f core.Finding) string {
	if f.File == "" {
		return ""
	}
	for _, p := range se.Projects {
		if p.Name == f.Project {
			return filepath.Join(p.Dir, filepath.FromSlash(f.File))
		}
	}
	if p, ok := se.Primary(); ok {
		return filepath.Join(p.Dir, filepath.FromSlash(f.File))
	}
	return ""
}

func reviewOf(se core.SessionState, f core.Finding) (core.AIReview, bool) {
	i := slices.IndexFunc(se.AIReviews, func(r core.AIReview) bool { return r.ID == f.Review })
	if i < 0 {
		return core.AIReview{}, false
	}
	return se.AIReviews[i], true
}

// reviewerLabel is a review's provider and model: "codex · gpt-5".
func reviewerLabel(r core.AIReview) string {
	if r.Model == "" {
		return r.Provider
	}
	return r.Provider + " · " + r.Model
}

// hasRepo reports whether the session has a project of the repo a finding names.
func hasRepo(se core.SessionState, repo string) bool {
	return slices.ContainsFunc(se.Projects, func(p core.Project) bool {
		return p.Name == repo || filepath.Base(p.Repo) == repo || filepath.Base(p.Dir) == repo
	})
}

const findHelp = "files/ai-review"

// view is the column: the review's header and severity chips, its findings worst first, and the reviewer's foot.
func (x *AIReview) view(c *ui.Ctx, root string, width float32) {
	th := c.Theme()
	se, _, inSession := kit.SessionAt(x.h.State(), root)
	last, reviewed := se.LastAIReview()
	c.Column("ai-review", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Role: ui.RoleRegion, Label: "AI review", Help: findHelp}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "AI REVIEW", kit.KickerFont(th.C(theme.FgFaint)))
			if reviewed {
				widget.Chip(c, "who", reviewerLabel(last), th.C(tFindReviewer), findHelp+"#reviewer")
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if s := last.Since(); s != "" {
				c.Text("since", "since "+s, kit.MonoFont(10, th.C(theme.FgGhost)))
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		var shown []core.Finding
		count := map[string]int{}
		gone := 0
		for _, f := range se.SortedFindings() {
			if f.Dismissed {
				gone++
			} else {
				count[f.Severity]++
			}
			if (!f.Dismissed || x.dismissed) && (x.filter == "" || f.Severity == x.filter) {
				shown = append(shown, f)
			}
		}
		if len(count) > 0 || gone > 0 {
			c.Row("counts", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 12, T: 8}, Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				for _, s := range core.Severities {
					n := count[s]
					if n == 0 {
						continue
					}
					label := fmt.Sprintf("%d %s", n, strings.ToUpper(kit.SeverityShort(s)))
					if widget.ToggleChip(c, s, label, x.filter == s, widget.ToggleChipOpts{Tone: kit.SeverityTone(th, s), Help: findHelp + "#severity",
						Tooltip: "Show only these"}) != (x.filter == s) {
						x.h.Run(context.Background(), "review.ai.filter", registry.Args{"severity": s})
					}
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if gone > 0 {
					label := fmt.Sprintf("%d dismissed", gone)
					if widget.Clickable(c, "dismissed", ui.Style{Dir: ui.Row, Label: label, Tooltip: "Show or hide them", Help: findHelp + "#dismiss"}, func(c *ui.Ctx) {
						fg := th.C(theme.FgFaint)
						if x.dismissed {
							fg = th.C(theme.Fg)
						}
						c.Text("t", label, kit.UIFont(11, fg))
					}).Clicked {
						x.h.Run(context.Background(), "review.ai.dismissed", nil)
					}
				}
			})
		}
		c.Column("cards", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{B: 10}}, func(c *ui.Ctx) {
			note := ""
			switch {
			case !inSession:
				note = "AI review runs in a session: this repo isn't one's."
			case reviewed && last.Running:
				note = kit.ProviderName(x.h, last.Provider) + " is reviewing the change…"
			case !reviewed:
				note = "No AI review yet. Review changes asks another model to read this change."
			case last.Error != "":
				note = "The review failed: " + last.Error
			case len(se.Findings) == 0:
				note = "No findings."
			}
			if note != "" {
				c.Row("note", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10)}, func(c *ui.Ctx) {
					widget.Rich(c, "t", []widget.Span{{Text: note}}, width-24, kit.UIFont(11.5, th.C(theme.FgFaint)), widget.RichOpts{})
				})
			}
			for _, f := range shown {
				x.card(c, se, f, width-42)
			}
		})
		c.Box("foot.rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 8}, func(c *ui.Ctx) {
			c.Row("who", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("k", "REVIEWER", kit.KickerFont(th.C(theme.FgFaint)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				what := "different model from the author"
				if reviewed && !last.Cross {
					what = "same model as the author"
				}
				c.Text("what", what, kit.MonoFont(10, th.C(theme.FgGhost)))
			})
			c.Row("acts", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				label := "Review changes"
				if reviewed {
					label = "Re-review changes"
				}
				args := registry.Args{"id": string(se.ID)}
				if widget.Button(c, "run", label, widget.ButtonOpts{Help: findHelp + "#reviewer", Busy: last.Running || x.asked[se.ID], BusyLabel: "Reviewing",
					Disabled: !inSession || !x.h.Available("review.ai.run", args)}) {
					if err := x.h.Run(context.Background(), "review.ai.run", args); err != nil {
						x.h.Log().Warn("ai review", "err", err)
					}
				}
				if widget.Button(c, "prompt", "Prompt…", widget.ButtonOpts{Ghost: true, Help: findHelp + "#prompt"}) {
					if err := x.h.Run(context.Background(), "review.ai.prompt", nil); err != nil {
						x.h.Log().Warn("ai review", "err", err)
					}
				}
			})
		})
	})
}

// card is one finding: its severity and category, title, place (a click shows it in the diff), body and actions.
func (x *AIReview) card(c *ui.Ctx, se core.SessionState, f core.Finding, width float32) {
	th := c.Theme()
	tone := kit.SeverityTone(th, f.Severity)
	fg, title := th.C(theme.FgFaint), th.C(theme.Fg)
	if f.Dismissed {
		title = th.C(theme.FgFaint)
	}
	run := func(cmd string, more registry.Args) {
		a := registry.Args{"id": string(se.ID), "finding": f.ID}
		for k, v := range more {
			a[k] = v
		}
		if err := x.h.Run(context.Background(), cmd, a); err != nil {
			x.h.Notify(core.Notice{ID: "review.ai", Severity: core.SevError, Key: true, Text: err.Error()})
		}
	}
	c.Box(f.ID, ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 10, T: 8}}, func(c *ui.Ctx) {
		c.Row("card", ui.Style{W: ui.Flex(1), Fill: th.C(tFindBg), Stroke: th.C(tFindLine), Border: 1, Radius: 7, Clip: true, Role: ui.RoleGroup,
			Label: f.Severity + ": " + f.Title, Help: findHelp + "#findings"}, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: tone, Float: &ui.Float{Fill: true}}, nil)
			c.Column("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 10, T: 9, B: 9}, Gap: 4}, func(c *ui.Ctx) {
				c.Row("kicker", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
					widget.Chip(c, "sev", kit.SeverityShort(f.Severity), tone, findHelp+"#severity")
					if f.Category != "" {
						widget.Chip(c, "cat", f.Category, th.C(theme.FgMuted), findHelp+"#findings")
					}
				})
				widget.Rich(c, "title", []widget.Span{{Text: f.Title}}, width, kit.UIFont(12.5, title), widget.RichOpts{})
				if w := f.Where(); w != "" {
					if widget.Clickable(c, "where", ui.Style{Dir: ui.Row, Label: w, Tooltip: "Show it in the diff", Help: findHelp + "#findings"}, func(c *ui.Ctx) {
						c.Text("t", w, kit.MonoFont(10.5, th.C(tFindWhere)))
					}).Clicked {
						run("review.ai.open", nil)
					}
				}
				if f.Body != "" {
					widget.Rich(c, "body", kit.MDSpans(f.Body), width, kit.UIFont(11.5, fg), widget.RichOpts{})
				}
				c.Row("acts", ui.Style{Gap: 6, Pad: ui.Insets{T: 3}, Cross: ui.Center}, func(c *ui.Ctx) {
					if f.Dismissed {
						if widget.Button(c, "restore", "Restore", widget.ButtonOpts{Ghost: true, Help: findHelp + "#dismiss"}) {
							run("review.ai.dismiss", registry.Args{"dismissed": false})
						}
						return
					}
					switch {
					case f.Repo != "" && !hasRepo(se, f.Repo):
						if widget.Button(c, "add", "Add project", widget.ButtonOpts{Help: findHelp + "#actions"}) {
							run("review.ai.add-project", nil)
						}
					case f.Thread != "":
						label := "Thread · open"
						if t, ok := se.Thread(f.Thread); ok && !t.Open() {
							label = "Thread · " + string(t.Status)
						}
						if widget.Button(c, "thread", label, widget.ButtonOpts{Help: findHelp + "#actions"}) {
							run("review.ai.thread", nil)
						}
					default:
						if widget.Button(c, "thread", "→ Thread", widget.ButtonOpts{Help: findHelp + "#actions"}) {
							run("review.ai.thread", nil)
						}
						if x.h.Available("review.ai.fix", nil) && widget.Button(c, "fix", "Fix", widget.ButtonOpts{Ghost: true, Help: findHelp + "#actions"}) {
							run("review.ai.fix", nil)
						}
					}
					if widget.Button(c, "dismiss", "Dismiss", widget.ButtonOpts{Ghost: true, Help: findHelp + "#dismiss"}) {
						run("review.ai.dismiss", nil)
					}
				})
				uireg.DrawThreadFeet(x.h, c, string(se.ID), f.ID, uireg.ThreadInFinding, width)
			})
		})
	})
}

// shipGate is the AI review as a "before this ships" gate: up to you until one runs, then met once every high and
// critical finding is dealt with.
func (x *AIReview) shipGate(id string) registry.Gate {
	se, ok := x.h.Session(core.SessionID(id))
	if !ok {
		return registry.Gate{}
	}
	s := se.Snapshot()
	pass, label := s.AIReviewGate()
	r, ran := s.LastAIReview()
	switch {
	case !ran:
		return registry.Gate{State: registry.GateOptional, Note: "optional", Action: "Review", Command: "review.ai.run", Args: registry.Args{"id": id}}
	case r.Running:
		return registry.Gate{State: registry.GatePending, Note: label}
	case pass:
		return registry.Gate{State: registry.GatePass, Note: label}
	}
	return registry.Gate{State: registry.GateWarn, Note: label}
}
