package host

import (
	"encoding/json"

	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/host/sandboxhost"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/agentrun"
	"github.com/liamg/oriel/host/aireview"
	"github.com/liamg/oriel/host/worktree"
)

// The host's API types live with the part that does the work; these are their names here, where the app finds them.

type EndReq = hostapi.EndReq

// Shared by the parts.
type (
	SandboxFactory = hostapi.SandboxFactory
	Approve        = hostapi.Approve
	Approval       = hostapi.Approval
	Sandbox        = hostapi.Sandbox
	SandboxAgent   = hostapi.SandboxAgent
	ToolsReq       = hostapi.ToolsReq
	Draft          = hostapi.Draft
)

// Agents.
type (
	App          = hostapi.App
	StartWorkReq = hostapi.StartWorkReq
	ConfigureReq = hostapi.ConfigureReq
	CommentReq   = hostapi.CommentReq
	ThreadReq    = hostapi.ThreadReq
	QueuedReq    = hostapi.QueuedReq
	UnqueueReq   = hostapi.UnqueueReq
	AnswerReq    = hostapi.AnswerReq
	AskReq       = hostapi.AskReq
	SendReq      = hostapi.SendReq
	TermReq      = hostapi.TermReq
	ToolCall     = hostapi.ToolCall
	Decide       = hostapi.Decide
	Verdict      = hostapi.Verdict
	Decision     = hostapi.Decision
)

// Workspaces.
type (
	Manifest      = hostapi.Manifest
	ManifestEntry = hostapi.ManifestEntry
	ManifestPR    = hostapi.ManifestPR
	NextPRReq     = hostapi.NextPRReq
	NewBranchReq  = hostapi.NewBranchReq
	PRMove        = hostapi.PRMove
)

// Sandboxes.
type (
	Permissions    = hostapi.Permissions
	ToolPermission = hostapi.ToolPermission
	Workspace      = hostapi.Workspace
	WorkspacePath  = hostapi.WorkspacePath
)

// Code hosts and the Ship page.
type (
	Ship            = hostapi.Ship
	ShipFile        = hostapi.ShipFile
	ShipCommit      = hostapi.ShipCommit
	ShipReq         = hostapi.ShipReq
	ShipRef         = hostapi.ShipRef
	ShipCommitReq   = hostapi.ShipCommitReq
	DraftReq        = hostapi.DraftReq
	RenameReq       = hostapi.RenameReq
	OffBranch       = hostapi.OffBranch
	Trust           = hostapi.Trust
	MirrorReq       = hostapi.MirrorReq
	Mirror          = hostapi.Mirror
	MirrorPR        = hostapi.MirrorPR
	ForwardReq      = hostapi.ForwardReq
	ForwardedReq    = hostapi.ForwardedReq
	DraftEditReq    = hostapi.DraftEditReq
	ResolveReq      = hostapi.ResolveReq
	PRSeenReq       = hostapi.PRSeenReq
	MirrorReviewReq = hostapi.MirrorReviewReq
	ReviewThread    = hostapi.ReviewThread
	ReviewComment   = hostapi.ReviewComment
)

// Repos.
type (
	RepoCheck     = hostapi.RepoCheck
	CloneReq      = hostapi.CloneReq
	CloneTarget   = hostapi.CloneTarget
	RepoFacts     = hostapi.RepoFacts
	StaleBranch   = hostapi.StaleBranch
	HousekeepPlan = hostapi.HousekeepPlan
	HousekeepReq  = hostapi.HousekeepReq
	Cleaned       = hostapi.Cleaned
	TagPreview    = hostapi.TagPreview
	TagReq        = hostapi.TagReq
	Tagged        = hostapi.Tagged
)

// Jobs, utility agents, AI reviews, stats.
type (
	Job             = hostapi.Job
	Schedule        = hostapi.Schedule
	Run             = hostapi.Run
	UtilitySpec     = hostapi.UtilitySpec
	UtilityEvent    = hostapi.UtilityEvent
	UtilityReq      = hostapi.UtilityReq
	Utility         = hostapi.Utility
	AIReviewReq     = hostapi.AIReviewReq
	StatsSessionReq = hostapi.StatsSessionReq
	UsageHistoryReq = hostapi.UsageHistoryReq
	SearchReq       = hostapi.SearchReq
	Hit             = hostapi.Hit
)

const (
	DraftByYou   = hostapi.DraftByYou
	AIReviewPack = hostapi.AIReviewPack
	BatchLead    = agentrun.BatchLead
	NoticeLead   = agentrun.NoticeLead
	NoPosting    = agentrun.NoPosting
	KeyWait      = agentrun.KeyWait
	ForAll       = agentrun.ForAll
	// An ask's answers (Decision).
	DecisionAllow   = hostapi.DecisionAllow
	DecisionAlways  = hostapi.DecisionAlways
	DecisionDeny    = hostapi.DecisionDeny
	DecisionAnswer  = hostapi.DecisionAnswer
	DecisionRecheck = hostapi.DecisionRecheck
	GatewayServer   = sandboxhost.GatewayServer
	ManifestFile    = hostapi.ManifestFile
	ReposDir        = hostapi.ReposDir

	SourceSession  = hostapi.SourceSession
	SourceProject  = hostapi.SourceProject
	SourceSettings = hostapi.SourceSettings
	SourceAlways   = hostapi.SourceAlways
	SourceReadOnly = hostapi.SourceReadOnly
	SourceDefault  = hostapi.SourceDefault
	SourceOriel    = hostapi.SourceOriel

	WhyMerged = hostapi.WhyMerged
	WhyGone   = hostapi.WhyGone

	EveryWeekday = hostapi.EveryWeekday
	EveryWeek    = hostapi.EveryWeek
	EveryDays    = hostapi.EveryDays
	RunRunning   = hostapi.RunRunning
	RunDone      = hostapi.RunDone
	RunBlocked   = hostapi.RunBlocked
	RunStopped   = hostapi.RunStopped
	RunSkipped   = hostapi.RunSkipped
)

var (
	// Templates a new repo can start from, besides any git URL (a GitHub template repo).
	Templates = hostapi.Templates
	// ErrKeyCancelled is a git operation stopped from its security-key card.
	ErrKeyCancelled = agentrun.ErrKeyCancelled
)

// Sanitise reduces a string to a name safe for a directory and a git branch.
func Sanitise(s string) string { return hostapi.Sanitise(s) }

// BranchName is <you>/<ticket>-<slug>.
func BranchName(you, ticket, slug string) string { return worktree.BranchName(you, ticket, slug) }

// LoadManifest reads root's manifest; ok is false when root isn't a workspace.
func LoadManifest(root string) (Manifest, bool) { return worktree.LoadManifest(root) }

// CloneName is the folder a clone of url is named by default: its last part, with .git dropped.
func CloneName(url string) string { return hostapi.CloneName(url) }

// JobOf is the saved job a session is a run of; "" for any other session.
func JobOf(se core.SessionState) string { return hostapi.JobOf(se) }

// RunOf reads a job session's run from its request thread.
func RunOf(se core.SessionState) Run { return hostapi.RunOf(se) }

// ParseFindings reads the findings a reviewer wrote as JSON in its answer.
func ParseFindings(text string) []core.Finding { return aireview.ParseFindings(text) }

// RuleFor is the rule "Always" makes of a tool call.
func RuleFor(tool string, input json.RawMessage) string { return agentrun.RuleFor(tool, input) }
