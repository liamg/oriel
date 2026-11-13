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

// The session host's calls, part by part, as the app's plugins make them: each part is the registry service of its
// own name ("host.sessions", "host.sandboxes"…), and a plugin takes only the part it uses.

// SessionCalls start, steer and end sessions: the registry's "host.sessions" service.
type SessionCalls interface {
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
	Fork(ctx context.Context, id core.SessionID, prompt string) (string, error)
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
	SetRestartOnExit(on bool)
	HandOver()
}

// SandboxCalls are the sessions' sandboxes and what they may do: the registry's "host.sandboxes" service.
type SandboxCalls interface {
	AddRule(kind core.AskKind, rule string) error
	RemoveRule(rule string) error
	Permissions(ctx context.Context, id core.SessionID) (Permissions, error)
	SetToolRule(id core.SessionID, tool, action string) error
	SetNetRule(id core.SessionID, host string, port int, action string, private bool) error
	RestartSandbox(id core.SessionID) error
	RebuildSandbox(ctx context.Context, id core.SessionID) error
	SandboxShell(ctx context.Context, id core.SessionID, cwd string) (sandbox.Command, error)
	SetSandboxConfig(cfg sandbox.Config) error
}

// ShipCalls review a session's work and ship it: the registry's "host.ships" service.
type ShipCalls interface {
	SetReviewed(r core.HunksReviewed) error
	SetReviewDraft(r core.ReviewDrafted) error
	SetFindings(r core.FindingsRecorded) error
	RecordVerified(v core.Verified) error
	AIReview(ctx context.Context, r AIReviewReq) (string, error)
	ShipRead(ctx context.Context, r ShipReq) ([]Ship, error)
	ShipDraft(ctx context.Context, r DraftReq) error
	ShipCommit(ctx context.Context, r ShipCommitReq) (string, error)
	ShipPush(ctx context.Context, r ShipRef) error
	ShipSync(ctx context.Context, r ShipRef) error
	ShipRename(ctx context.Context, r RenameReq) error
	ShipSwitchBack(ctx context.Context, r ShipRef) error
	ShipAdopt(ctx context.Context, r ShipRef) error
	NewBranch(ctx context.Context, r NewBranchReq) (string, error)
	ShipMergeDefault(ctx context.Context, r ShipRef) (gitx.Merge, error)
	NextPR(ctx context.Context, r NextPRReq) error
	UpdatePR(ctx context.Context, id core.SessionID) (map[string]PRMove, error)
}

// CodeHostCalls are the code hosts' pull requests and review threads: the registry's "host.codehosts" service.
type CodeHostCalls interface {
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
	ReactPR(ctx context.Context, r PRReactReq) error
	UnreactPR(ctx context.Context, r PRReactReq) error
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
}

// RepoCalls add, clone, tidy and tag the host's repositories: the registry's "host.repos" service.
type RepoCalls interface {
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
}

// JobCalls are the scheduled jobs: the registry's "host.jobs" service.
type JobCalls interface {
	Jobs() []Job
	SaveJob(j Job) (Job, error)
	DeleteJob(name string) error
	RunJob(ctx context.Context, name string) (core.SessionID, error)
}

// StatsCalls read the agents' usage and the sessions' stats: the registry's "host.stats" service.
type StatsCalls interface {
	ReadUsage(ctx context.Context) error
	StatsQuery(q stats.Query) stats.Report
	SessionStats(r StatsSessionReq) (stats.Detail, error)
	UsageHistory(r UsageHistoryReq) []core.ProviderUsage
}
