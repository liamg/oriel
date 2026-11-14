package agentrun

import (
	"github.com/liamg/oriel/host/hostapi"
)

type StartWorkReq = hostapi.StartWorkReq

type ConfigureReq = hostapi.ConfigureReq

type CommentReq = hostapi.CommentReq

type ThreadReq = hostapi.ThreadReq

type AnswerReq = hostapi.AnswerReq

type AskReq = hostapi.AskReq

type SendReq = hostapi.SendReq

type TermReq = hostapi.TermReq

type ToolCall = hostapi.ToolCall

type Decide = hostapi.Decide

type Decision = hostapi.Decision

const (
	DecisionAllow   = hostapi.DecisionAllow
	DecisionAlways  = hostapi.DecisionAlways
	DecisionDeny    = hostapi.DecisionDeny
	DecisionAnswer  = hostapi.DecisionAnswer
	DecisionRecheck = hostapi.DecisionRecheck
)

type Verdict = hostapi.Verdict
