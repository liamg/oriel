package registry

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
)

// ForkRequest is a side question for a session's agent: answered from what it knows of the session, with no tools,
// outside its conversation: btw, explain, the verify quiz.
type ForkRequest struct {
	Session string
	Prompt  string
}

// Forker answers side questions for sessions. The agent's (a tool-less fork of the session's) registers one; a
// plugin can put its own first.
type Forker struct {
	Name   string
	Fork   func(ctx context.Context, r ForkRequest) (string, error)
	Order  int                         // the first by Order answers
	Label  func(session string) string // who answers: "claude · fork, no tools"
	Source string
}

func (Forker) ContributionKind() string   { return "forker" }
func (f Forker) ContributionName() string { return f.Name }

// Forkers are the forkers by Order, then name.
func Forkers(r Contributions) []Forker {
	var out []Forker
	out = append(out, ContributionsOf[Forker](r)...)
	slices.SortFunc(out, func(a, b Forker) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// Fork asks the first forker r's question, and says which answered.
func Fork(ctx context.Context, reg Registry, r ForkRequest) (string, Forker, error) {
	fs := Forkers(reg)
	if len(fs) == 0 {
		return "", Forker{}, errors.New("nothing can answer a side question yet: it needs an agent")
	}
	var out string
	err := Call(func() (err error) { out, err = fs[0].Fork(ctx, r); return err })
	return out, fs[0], err
}

// ForkLabel is who answers a session's side questions, "" when nothing can.
func ForkLabel(reg Contributions, session string) string {
	fs := Forkers(reg)
	if len(fs) == 0 || fs[0].Label == nil {
		return ""
	}
	var out string
	Call(func() error { out = fs[0].Label(session); return nil })
	return out
}
