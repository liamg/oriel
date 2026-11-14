package hostapi

import (
	"github.com/liamg/oriel/core"
)

// AIReviewReq asks for a cross-model AI review of a session's change.
type AIReviewReq struct {
	Session  core.SessionID `json:"session"`
	Provider string         `json:"provider,omitempty"` // the reviewer's; "" for the session agent's
	Model    string         `json:"model,omitempty"`    // "" for the provider's default
	// Full reviews the whole change since the base; otherwise a session reviewed before has what changed since.
	Full bool `json:"full,omitempty"`
	// Pack is the reviewer's prompt, when it isn't the built-in ai-review pack: the user's own.
	Pack string `json:"pack,omitempty"`
}

// AIReviewPack is the prompt pack an AI review's reviewer runs on.
const AIReviewPack = "ai-review"
