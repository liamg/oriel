package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/sandbox"
)

// The host's API is the hostapi contracts the app reaches it through, written out here: each method hands the call to
// the part that does the work. A part's other methods stay its own, for the host to wire (host.go).
var (
	_ hostapi.SessionCalls  = (*Host)(nil)
	_ hostapi.Wiring        = (*Host)(nil)
	_ hostapi.SandboxCalls  = (*Host)(nil)
	_ hostapi.ShipCalls     = (*Host)(nil)
	_ hostapi.CodeHostCalls = (*Host)(nil)
	_ hostapi.RepoCalls     = (*Host)(nil)
	_ hostapi.JobCalls      = (*Host)(nil)
	_ hostapi.StatsCalls    = (*Host)(nil)
)

// RunSchedules runs the scheduled jobs every interval until ctx ends: while the host is awake.
func (w *Host) RunSchedules(ctx context.Context, every time.Duration) {
	w.jobs.RunSchedules(ctx, every)
}

// RunDue starts every scheduled job due now: one pass of RunSchedules.
func (w *Host) RunDue(ctx context.Context) { w.jobs.RunDue(ctx) }

// Prompts is the user's versions of the prompts the app last sent (SetPrompts), and whether it has.
func (w *Host) Prompts() (prompts.Overrides, bool) { return w.run.Prompts() }

// The host's hostapi.SessionCalls

func (w *Host) Interrupt(id core.SessionID) error { return w.run.Interrupt(id) }
func (w *Host) StopProcess(ctx context.Context, r hostapi.StopProcessReq) error {
	return w.run.StopProcess(ctx, r)
}
func (w *Host) Fork(ctx context.Context, r hostapi.ForkReq) (string, error) {
	return w.run.Fork(ctx, r)
}
func (w *Host) Send(r hostapi.SendReq) error     { return w.run.Send(r) }
func (w *Host) Answer(r hostapi.AnswerReq) error { return w.run.Answer(r) }
func (w *Host) StartLogin(ctx context.Context, provider string) (string, error) {
	return w.run.StartLogin(ctx, provider)
}
func (w *Host) Ask(ctx context.Context, r hostapi.AskReq) (hostapi.Verdict, error) {
	return w.run.Ask(ctx, r)
}
func (w *Host) Comment(ctx context.Context, r hostapi.CommentReq) (core.ThreadID, error) {
	return w.run.Comment(ctx, r)
}
func (w *Host) SetThread(r hostapi.ThreadReq) error { return w.run.SetThread(r) }
func (w *Host) Unqueue(r hostapi.UnqueueReq) error  { return w.run.Unqueue(r) }
func (w *Host) Workspace(ctx context.Context, id core.SessionID) (hostapi.Workspace, error) {
	return w.boxes.Workspace(ctx, id)
}
func (w *Host) Terminal(ctx context.Context, id core.SessionID) (<-chan []byte, error) {
	return w.run.Terminal(ctx, id)
}
func (w *Host) TermInput(r hostapi.TermReq) error { return w.run.TermInput(r) }
func (w *Host) StartUtility(ctx context.Context, spec hostapi.UtilitySpec) (hostapi.Utility, error) {
	return w.utility.StartUtility(ctx, spec)
}
func (w *Host) SetPrompts(o prompts.Overrides) error { return w.run.SetPrompts(o) }
func (w *Host) SetRestartOnExit(on bool)             { w.run.SetRestartOnExit(on) }

// The host's hostapi.SandboxCalls

func (w *Host) AddRule(kind core.AskKind, rule string) error    { return w.run.AddRule(kind, rule) }
func (w *Host) RemoveRule(kind core.AskKind, rule string) error { return w.run.RemoveRule(kind, rule) }
func (w *Host) Permissions(ctx context.Context, id core.SessionID) (hostapi.Permissions, error) {
	return w.boxes.Permissions(ctx, id)
}
func (w *Host) SetToolRule(id core.SessionID, tool string, action hostapi.ToolAction) error {
	return w.boxes.SetToolRule(id, tool, action)
}
func (w *Host) SetNetRule(r hostapi.NetRuleReq) error {
	return w.boxes.SetNetRule(r)
}
func (w *Host) RestartSandbox(id core.SessionID) error { return w.boxes.RestartSandbox(id) }
func (w *Host) RebuildSandbox(ctx context.Context, id core.SessionID) error {
	return w.boxes.RebuildSandbox(ctx, id)
}
func (w *Host) SandboxShell(ctx context.Context, id core.SessionID, cwd string) (sandbox.Command, error) {
	return w.boxes.SandboxShell(ctx, id, cwd)
}

// The host's hostapi.ShipCalls

func (w *Host) SetReviewDraft(r core.ReviewDrafted) error { return w.code.SetReviewDraft(r) }
func (w *Host) SetFindings(r core.FindingsRecorded) error { return w.reviews.SetFindings(r) }
func (w *Host) AIReview(ctx context.Context, r hostapi.AIReviewReq) (string, error) {
	return w.reviews.AIReview(ctx, r)
}
func (w *Host) ShipRead(ctx context.Context, r hostapi.ShipReq) ([]hostapi.Ship, error) {
	return w.code.ShipRead(ctx, r)
}
func (w *Host) ShipDraft(ctx context.Context, r hostapi.DraftReq) error {
	return w.code.ShipDraft(ctx, r)
}
func (w *Host) ShipCommit(ctx context.Context, r hostapi.ShipCommitReq) (string, error) {
	return w.code.ShipCommit(ctx, r)
}
func (w *Host) ShipPush(ctx context.Context, r hostapi.ShipRef) error { return w.code.ShipPush(ctx, r) }
func (w *Host) ShipSync(ctx context.Context, r hostapi.ShipRef) error { return w.code.ShipSync(ctx, r) }
func (w *Host) ShipMergeDefault(ctx context.Context, r hostapi.ShipRef) (gitx.Merge, error) {
	return w.code.ShipMergeDefault(ctx, r)
}
func (w *Host) UpdatePR(ctx context.Context, r hostapi.UpdatePRReq) (map[string]hostapi.PRMove, error) {
	return w.work.UpdatePR(ctx, r)
}
func (w *Host) ShipBranch(ctx context.Context, r hostapi.ShipRef) (string, error) {
	return w.code.ShipBranch(ctx, r)
}
func (w *Host) SawPR(r hostapi.PRSeenReq) error { return w.code.SawPR(r) }

// The host's hostapi.CodeHostCalls

func (w *Host) ProjectRemote(ctx context.Context, r hostapi.ShipRef) (string, error) {
	return w.code.ProjectRemote(ctx, r)
}
func (w *Host) MarkHostResolved(r hostapi.ResolveReq) error { return w.code.MarkHostResolved(r) }
func (w *Host) MirrorReview(ctx context.Context, r hostapi.MirrorReviewReq) error {
	return w.code.MirrorReview(ctx, r)
}
func (w *Host) DraftOf(r hostapi.ForwardReq) (core.Thread, string, error) { return w.code.DraftOf(r) }
func (w *Host) EditDraft(r hostapi.DraftEditReq) error                    { return w.code.EditDraft(r) }
func (w *Host) ForwardHost(ctx context.Context, r hostapi.ForwardReq, post func(ctx context.Context, t core.Thread, body string) (string, error)) (core.Comment, error) {
	return w.code.ForwardHost(ctx, r, post)
}

// The host's hostapi.RepoCalls

func (w *Host) Repos() []core.Repo { return w.repos.Repos() }
func (w *Host) AddRepoPath(ctx context.Context, path string) (core.Repo, error) {
	return w.repos.AddRepoPath(ctx, path)
}
func (w *Host) CheckRepoPath(ctx context.Context, p string) hostapi.RepoCheck {
	return w.repos.CheckRepoPath(ctx, p)
}
func (w *Host) CloneParent() string { return w.repos.CloneParent() }
func (w *Host) CloneTarget(ctx context.Context, r hostapi.CloneReq) (hostapi.CloneTarget, error) {
	return w.repos.CloneTarget(ctx, r)
}
func (w *Host) CloneRepo(ctx context.Context, r hostapi.CloneReq) (core.Repo, error) {
	return w.repos.CloneRepo(ctx, r)
}
func (w *Host) FetchRepo(ctx context.Context, id core.RepoID) error {
	return w.repos.FetchRepo(ctx, id)
}
func (w *Host) RemoveRepo(id core.RepoID) error { return w.repos.RemoveRepo(id) }
func (w *Host) ScanRepos(ctx context.Context, all bool) ([]core.Repo, error) {
	return w.repos.ScanRepos(ctx, all)
}
func (w *Host) SetReposDir(dir string) { w.repos.SetReposDir(dir) }
func (w *Host) RepoFacts(ctx context.Context, id core.RepoID) (hostapi.RepoFacts, error) {
	return w.repos.RepoFacts(ctx, id)
}
func (w *Host) Housekeeping(ctx context.Context, req hostapi.HousekeepReq) (hostapi.HousekeepPlan, error) {
	return w.repos.Housekeeping(ctx, req)
}
func (w *Host) Housekeep(ctx context.Context, req hostapi.HousekeepReq) ([]hostapi.Cleaned, error) {
	return w.repos.Housekeep(ctx, req)
}
func (w *Host) TagPreview(ctx context.Context, id core.RepoID, branch string) (hostapi.TagPreview, error) {
	return w.repos.TagPreview(ctx, id, branch)
}
func (w *Host) Tag(ctx context.Context, req hostapi.TagReq) (hostapi.Tagged, error) {
	return w.repos.Tag(ctx, req)
}
func (w *Host) PushTag(ctx context.Context, id core.RepoID, name string) error {
	return w.repos.PushTag(ctx, id, name)
}

// The host's hostapi.JobCalls

func (w *Host) Jobs() []hostapi.Job                        { return w.jobs.Jobs() }
func (w *Host) SaveJob(j hostapi.Job) (hostapi.Job, error) { return w.jobs.SaveJob(j) }
func (w *Host) DeleteJob(name string) error                { return w.jobs.DeleteJob(name) }
func (w *Host) RunJob(ctx context.Context, name string) (core.SessionID, error) {
	return w.jobs.RunJob(ctx, name)
}

// The host's hostapi.StatsCalls

func (w *Host) ReadUsage(ctx context.Context) error { return w.stats.ReadUsage(ctx) }
func (w *Host) UsageHistory(r hostapi.UsageHistoryReq) []core.ProviderUsage {
	return w.stats.UsageHistory(r)
}
