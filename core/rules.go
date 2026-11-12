package core

import (
	"slices"
	"time"
)

func init() {
	RegisterEvent[RulesChanged]("RulesChanged")
}

// Rule lets a kind of call through in every session without a card: what "Allow for all sessions" makes of an ask.
// It's written as a session's rules are: Bash(go test:*), Edit, a gateway's tool, net(*.golang.org:443).
type Rule struct {
	Rule  string    `json:"rule"`
	Kind  AskKind   `json:"kind"` // the asks it answers: permission, gateway or network
	Added time.Time `json:"added,omitzero"`
}

// RulesChanged is the rules for all sessions as they are now.
type RulesChanged struct {
	Rules []Rule `json:"rules"`
}

func (e RulesChanged) Scope() SessionID { return "" }

func (e RulesChanged) Apply(s *State) error {
	s.Rules = slices.Clone(e.Rules)
	return nil
}
