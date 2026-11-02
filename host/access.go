package host

import (
	"context"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/sandbox"
)

// Access is the session host as the app's plugins call it: the registry's "host" service.
type Access interface {
	// Sessions
	Start(ctx context.Context, spec core.SessionSpec) (core.SessionID, error)
	StartWork(ctx context.Context, r StartWorkReq) error
	Stop(ctx context.Context, id core.SessionID) error
	Resume(ctx context.Context, id core.SessionID) error
	Archive(ctx context.Context, id core.SessionID) error
	Delete(ctx context.Context, id core.SessionID) error
	End(ctx context.Context, r EndReq) error
	Configure(ctx context.Context, r ConfigureReq) error
	Interrupt(id core.SessionID) error
	StopProcess(ctx context.Context, id core.SessionID, process string) error
	Rename(id core.SessionID, name string) error
	Fork(id core.SessionID, prompt string) (string, error)
	AddRepo(ctx context.Context, id core.SessionID, ref core.RepoID) error
	Send(r SendReq) error
	Answer(r AnswerReq) error
	StartLogin(ctx context.Context, provider string) (string, error)
	Ask(ctx context.Context, r AskReq) (Verdict, error)
	Comment(ctx context.Context, r CommentReq) (core.ThreadID, error)
	SetThread(r ThreadReq) error
	EditQueued(r QueuedReq) error
	Unqueue(r UnqueueReq) error
	Workspace(ctx context.Context, id core.SessionID) (Workspace, error)
	Terminal(ctx context.Context, id core.SessionID) (<-chan []byte, error)
	TermInput(r TermReq) error
	ProviderInfo(ctx context.Context) []ProviderInfo
	StartUtility(ctx context.Context, spec UtilitySpec) (*Utility, error)
	Attach(r AttachReq) (string, error)
	SetPrompts(o prompts.Overrides) error
	HandOver()

	// Sandboxes and permissions
	Permissions(ctx context.Context, id core.SessionID) (Permissions, error)
	SetToolRule(id core.SessionID, tool, action string) error
	SetNetRule(id core.SessionID, host string, port int, action string, private bool) error
	RestartSandbox(id core.SessionID) error
	RebuildSandbox(ctx context.Context, id core.SessionID) error
	SandboxShell(ctx context.Context, id core.SessionID, cwd string) (sandbox.Command, error)
	SetSandboxConfig(cfg sandbox.Config) error

	// Review and ship
	SetReviewed(r core.HunksReviewed) error
	SetSeen(r core.LinesSeen) error
	SetReviewDraft(r core.ReviewDrafted) error
	SetFindings(r core.FindingsRecorded) error
	RecordOverride(r core.CommitGateOverridden) error
	RecordVerified(v core.Verified) error
	AIReview(ctx context.Context, r AIReviewReq) (string, error)
	ShipRead(ctx context.Context, r ShipReq) ([]Ship, error)
	ShipDraft(ctx context.Context, r DraftReq) error
	ShipCommit(ctx context.Context, r ShipCommitReq) (string, error)
	ShipPush(ctx context.Context, r ShipRef) error
	ShipSync(ctx context.Context, r ShipRef) error
	ShipRename(ctx context.Context, r RenameReq) error
	ShipMergeDefault(ctx context.Context, r ShipRef) (gitx.Merge, error)
	NextPR(ctx context.Context, r NextPRReq) error
	UpdatePR(ctx context.Context, id core.SessionID) (map[string]PRMove, error)

	// Code hosts
	SyncGitHub(ctx context.Context, r MirrorReq) (Mirror, error)
	ResolveGitHub(ctx context.Context, r ResolveReq) error
	MarkHostResolved(r ResolveReq) error
	MirrorReview(ctx context.Context, r MirrorReviewReq) error
	DraftOf(r ForwardReq) (core.Thread, string, error)
	EditDraft(r DraftEditReq) error
	ForwardGitHub(ctx context.Context, r ForwardReq) (core.Comment, error)
	ForwardHost(ctx context.Context, r ForwardReq, post func(ctx context.Context, t core.Thread, body string) (string, error)) (core.Comment, error)
	PRReview(ctx context.Context, r PRReviewReq) (PRReview, error)
	SubmitPRReview(ctx context.Context, r PRSubmitReq) error
	ReviewQueue(ctx context.Context, r ReviewQueueReq) ([]gh.Asked, error)
	SearchPRs(ctx context.Context, r PRsReq) ([]gh.Found, error)
	GitHubPRFor(ctx context.Context, r GitHubReq) (*gh.PR, error)
	GitHubCreate(ctx context.Context, r GitHubReq) (*gh.PR, error)
	GitHubEdit(ctx context.Context, r GitHubReq) (*gh.PR, error)
	GitHubReady(ctx context.Context, r GitHubReq) error
	GitHubReviewers(ctx context.Context, r GitHubReq) error
	GitHubChecks(ctx context.Context, r GitHubReq) (GitHubChecks, error)
	GitHubMergeState(ctx context.Context, r GitHubReq) (gh.MergeState, error)
	GitHubMerge(ctx context.Context, r GitHubReq) error
	GitHubDeleteBranch(ctx context.Context, r GitHubReq) error

	// Repos
	Repos() []core.Repo
	AddRepoPath(ctx context.Context, path string) (core.Repo, error)
	CheckRepoPath(ctx context.Context, p string) RepoCheck
	CloneParent() string
	CloneTarget(ctx context.Context, r CloneReq) (CloneTarget, error)
	CloneRepo(ctx context.Context, r CloneReq) (core.Repo, error)
	FetchRepo(ctx context.Context, id core.RepoID) error
	RemoveRepo(id core.RepoID) error
	ScanRepos(ctx context.Context, all bool) ([]core.Repo, error)
	SetReposDir(dir string)
	RepoFacts(ctx context.Context, id core.RepoID) (RepoFacts, error)
	Housekeeping(ctx context.Context, req HousekeepReq) (HousekeepPlan, error)
	Housekeep(ctx context.Context, req HousekeepReq) ([]Cleaned, error)
	TagPreview(ctx context.Context, id core.RepoID, branch string) (TagPreview, error)
	Tag(ctx context.Context, req TagReq) (Tagged, error)
	PushTag(ctx context.Context, id core.RepoID, name string) error

	// Jobs
	Jobs() []Job
	SaveJob(j Job) (Job, error)
	DeleteJob(name string) error
	RunJob(ctx context.Context, name string) (core.SessionID, error)

	// Stats and usage
	ReadUsage(ctx context.Context) error
	StatsQuery(q stats.Query) stats.Report
	SessionStats(r StatsSessionReq) (stats.Detail, error)
	UsageHistory(r UsageHistoryReq) []core.ProviderUsage
}

var _ Access = (*Host)(nil)
