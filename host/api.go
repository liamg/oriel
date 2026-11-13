package host

import (
	"encoding/json"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/agentrun"
	"github.com/liamg/oriel/host/aireview"
	"github.com/liamg/oriel/host/github"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/host/jobs"
	"github.com/liamg/oriel/host/persist"
	"github.com/liamg/oriel/host/repos"
	"github.com/liamg/oriel/host/sandboxhost"
	"github.com/liamg/oriel/host/utility"
	"github.com/liamg/oriel/host/worktree"
)

// The host's API types live with the part that does the work; these are their names here, where the app finds them.

// EndReq ends a session: archived, or deleted.
type EndReq struct {
	Session      core.SessionID `json:"session"`
	Delete       bool           `json:"delete,omitempty"`       // throw it all away; otherwise archive
	Discard      bool           `json:"discard,omitempty"`      // archive over uncommitted work, which is lost
	DeleteBranch bool           `json:"deleteBranch,omitempty"` // each project's local branch goes too
}

// Shared by the parts.
type (
	SandboxFactory = hostcore.SandboxFactory
	Approve        = hostcore.Approve
	Approval       = hostcore.Approval
	Sandbox        = hostcore.Sandbox
	SandboxAgent   = hostcore.SandboxAgent
	ToolsReq       = hostcore.ToolsReq
	Draft          = hostcore.Draft
)

// Agents.
type (
	App          = agentrun.App
	StartWorkReq = agentrun.StartWorkReq
	ConfigureReq = agentrun.ConfigureReq
	CommentReq   = agentrun.CommentReq
	ThreadReq    = agentrun.ThreadReq
	QueuedReq    = agentrun.QueuedReq
	UnqueueReq   = agentrun.UnqueueReq
	AnswerReq    = agentrun.AnswerReq
	AskReq       = agentrun.AskReq
	SendReq      = agentrun.SendReq
	TermReq      = agentrun.TermReq
	ToolCall     = agentrun.ToolCall
	Decide       = agentrun.Decide
	Verdict      = agentrun.Verdict
	Decision     = agentrun.Decision
)

// Workspaces.
type (
	Manifest      = worktree.Manifest
	ManifestEntry = worktree.ManifestEntry
	ManifestPR    = worktree.ManifestPR
	NextPRReq     = worktree.NextPRReq
	NewBranchReq  = worktree.NewBranchReq
	PRMove        = worktree.PRMove
)

// Sandboxes.
type (
	Permissions    = sandboxhost.Permissions
	ToolPermission = sandboxhost.ToolPermission
	Workspace      = sandboxhost.Workspace
	WorkspacePath  = sandboxhost.WorkspacePath
)

// Code hosts and the Ship page.
type (
	Ship            = github.Ship
	ShipFile        = github.ShipFile
	ShipCommit      = github.ShipCommit
	ShipReq         = github.ShipReq
	ShipRef         = github.ShipRef
	ShipCommitReq   = github.ShipCommitReq
	DraftReq        = github.DraftReq
	RenameReq       = github.RenameReq
	OffBranch       = hostcore.OffBranch
	PRsReq          = github.PRsReq
	ReviewQueueReq  = github.ReviewQueueReq
	PRReview        = github.PRReview
	PRReviewReq     = github.PRReviewReq
	PRSubmitReq     = github.PRSubmitReq
	PRReactReq      = github.PRReactReq
	Trust           = github.Trust
	MirrorReq       = github.MirrorReq
	Mirror          = github.Mirror
	MirrorPR        = github.MirrorPR
	ForwardReq      = github.ForwardReq
	ForwardedReq    = github.ForwardedReq
	DraftEditReq    = github.DraftEditReq
	ResolveReq      = github.ResolveReq
	GitHubReq       = github.GitHubReq
	GitHubChecks    = github.GitHubChecks
	MirrorReviewReq = github.MirrorReviewReq
	ReviewThread    = github.ReviewThread
	ReviewComment   = github.ReviewComment
)

// Repos.
type (
	RepoCheck     = repos.RepoCheck
	CloneReq      = repos.CloneReq
	CloneTarget   = repos.CloneTarget
	RepoFacts     = repos.RepoFacts
	StaleBranch   = repos.StaleBranch
	HousekeepPlan = repos.HousekeepPlan
	HousekeepReq  = repos.HousekeepReq
	Cleaned       = repos.Cleaned
	TagPreview    = repos.TagPreview
	TagReq        = repos.TagReq
	Tagged        = repos.Tagged
)

// Jobs, utility agents, AI reviews, stats.
type (
	Job             = jobs.Job
	Schedule        = jobs.Schedule
	Run             = jobs.Run
	UtilitySpec     = utility.UtilitySpec
	UtilityEvent    = utility.UtilityEvent
	UtilityReq      = utility.UtilityReq
	Utility         = utility.Utility
	AIReviewReq     = aireview.AIReviewReq
	StatsSessionReq = persist.StatsSessionReq
	UsageHistoryReq = persist.UsageHistoryReq
	SearchReq       = persist.SearchReq
	Hit             = persist.Hit
)

const (
	DraftByYou    = hostcore.DraftByYou
	AIReviewPack  = aireview.AIReviewPack
	BatchLead     = agentrun.BatchLead
	NoticeLead    = agentrun.NoticeLead
	NoPosting     = agentrun.NoPosting
	KeyWait       = agentrun.KeyWait
	ForAll        = agentrun.ForAll
	// An ask's answers (Decision).
	DecisionAllow   = agentrun.DecisionAllow
	DecisionAlways  = agentrun.DecisionAlways
	DecisionDeny    = agentrun.DecisionDeny
	DecisionAnswer  = agentrun.DecisionAnswer
	DecisionRecheck = agentrun.DecisionRecheck
	GatewayServer = sandboxhost.GatewayServer
	ManifestFile  = worktree.ManifestFile
	ReposDir      = worktree.ReposDir

	SourceSession  = sandboxhost.SourceSession
	SourceProject  = sandboxhost.SourceProject
	SourceSettings = sandboxhost.SourceSettings
	SourceAlways   = sandboxhost.SourceAlways
	SourceReadOnly = sandboxhost.SourceReadOnly
	SourceDefault  = sandboxhost.SourceDefault
	SourceOriel    = sandboxhost.SourceOriel

	WhyMerged = repos.WhyMerged
	WhyGone   = repos.WhyGone

	EveryWeekday = jobs.EveryWeekday
	EveryWeek    = jobs.EveryWeek
	EveryDays    = jobs.EveryDays
	RunRunning   = jobs.RunRunning
	RunDone      = jobs.RunDone
	RunBlocked   = jobs.RunBlocked
	RunStopped   = jobs.RunStopped
	RunSkipped   = jobs.RunSkipped
)

var (
	// NotesFiles are the agent notes generated at a workspace's root: CLAUDE.md for Claude, AGENTS.md for Codex.
	NotesFiles = hostcore.NotesFiles
	// Templates a new repo can start from, besides any git URL (a GitHub template repo).
	Templates = worktree.Templates
	// ErrKeyCancelled is a git operation stopped from its security-key card.
	ErrKeyCancelled = agentrun.ErrKeyCancelled
)

// Sanitise reduces a string to a name safe for a directory and a git branch.
func Sanitise(s string) string { return hostcore.Sanitise(s) }

// BranchName is <you>/<ticket>-<slug>.
func BranchName(you, ticket, slug string) string { return worktree.BranchName(you, ticket, slug) }

// LoadManifest reads root's manifest; ok is false when root isn't a workspace.
func LoadManifest(root string) (Manifest, bool) { return worktree.LoadManifest(root) }

// CloneName is the folder a clone of url is named by default: its last part, with .git dropped.
func CloneName(url string) string { return repos.CloneName(url) }

// JobOf is the saved job a session is a run of; "" for any other session.
func JobOf(se core.SessionState) string { return jobs.JobOf(se) }

// RunOf reads a job session's run from its request thread.
func RunOf(se core.SessionState) Run { return jobs.RunOf(se) }

// ParseFindings reads the findings a reviewer wrote as JSON in its answer.
func ParseFindings(text string) []core.Finding { return aireview.ParseFindings(text) }

// RuleFor is the rule "Always" makes of a tool call.
func RuleFor(tool string, input json.RawMessage) string { return agentrun.RuleFor(tool, input) }
