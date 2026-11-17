// Package policy is what the gateway decides about a tool, as plain values: the decision, where it came from, and
// the tool it's about. Contracts carry these without reaching the gateway or its MCP server.
package policy

// The levels a decision can come from.
const (
	FromSession  = "session"
	FromProject  = "project"
	FromGlobal   = "global"
	FromReadOnly = "read-only"
	FromDefault  = "default"
)

// Why is a decision and what made it: the level, and the rule's pattern.
type Why struct {
	Action string `json:"action"`
	From   string `json:"from"`
	Rule   string `json:"rule,omitempty"`
}

// ToolPolicy is one tool and what a call to it would meet.
type ToolPolicy struct {
	Name        string `json:"name"`
	Server      string `json:"server"`
	Description string `json:"description,omitempty"`
	Builtin     bool   `json:"builtin,omitempty"`
	ReadOnly    bool   `json:"readOnly,omitempty"`
	// Fixed tools take no rule: one that asks by nature, or does its own asking.
	Fixed bool `json:"fixed,omitempty"`
	Why
}
