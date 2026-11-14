package hostapi

import (
	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// EndReq ends a session: archived, or deleted.
type EndReq struct {
	Session      core.SessionID `json:"session"`
	Delete       bool           `json:"delete,omitempty"`       // throw it all away; otherwise archive
	Discard      bool           `json:"discard,omitempty"`      // archive over uncommitted work, which is lost
	DeleteBranch bool           `json:"deleteBranch,omitempty"` // each project's local branch goes too
}

// ProviderInfo is a provider as the launcher shows it.
type ProviderInfo struct {
	ID      string             `json:"id"`
	Install agent.Install      `json:"install"`
	Has     agent.Features     `json:"has"` // what its sessions can do past the least (agent.Featured)
	Models  []agent.Model      `json:"models,omitempty"`
	Usage   core.ProviderUsage `json:"usage,omitzero"`
	Error   string             `json:"error,omitempty"`
}

// AttachReq is a file for a session's agent: Data (a pasted picture) named Name, or a copy of the file at Path.
type AttachReq struct {
	Session core.SessionID `json:"session"`
	Name    string         `json:"name,omitempty"`
	Data    []byte         `json:"data,omitempty"`
	Path    string         `json:"path,omitempty"`
}
