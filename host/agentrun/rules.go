package agentrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/atomicfile"
)

// ForAll is AnswerReq.For's "all sessions": an always that makes a rule every session meets.
const ForAll = "all"

// allRules are the rules for all sessions, kept in rules.json in Oriel's home and mirrored in the state.
type allRules struct {
	path string
	mu   sync.Mutex
	list []core.Rule
}

// rulesFile is rules.json.
type rulesFile struct {
	Rules []core.Rule `json:"rules"`
}

func (r *allRules) load() error {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f rulesFile
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	r.mu.Lock()
	r.list = f.Rules
	r.mu.Unlock()
	return nil
}

func (r *allRules) all() []core.Rule {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

// edit changes the list and saves it; on an error it's left as it was.
func (r *allRules) edit(fn func([]core.Rule) ([]core.Rule, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, err := fn(slices.Clone(r.list))
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(rulesFile{Rules: next}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	if err := atomicfile.Write(r.path, append(b, '\n'), 0o600); err != nil {
		return err
	}
	r.list = next
	return nil
}

// Rules are the rules for all sessions.
func (x *Runner) Rules() []core.Rule { return x.rules.all() }

// RulesChanged puts the rules for all sessions in the state.
func (x *Runner) RulesChanged() {
	_ = x.st.Emit(core.RulesChanged{Rules: x.rules.all()})
}

// AddRule makes rule one for all sessions, answering asks of kind in every session before a card, from now on and
// after a restart.
func (x *Runner) AddRule(kind core.AskKind, rule string) error {
	if rule == "" {
		return errors.New("a rule needs something to allow")
	}
	switch kind {
	case core.AskPermission, core.AskGateway, core.AskNetwork:
	default:
		return fmt.Errorf("%q asks don't take rules", kind)
	}
	err := x.rules.edit(func(rs []core.Rule) ([]core.Rule, error) {
		if slices.ContainsFunc(rs, func(r core.Rule) bool { return r.Rule == rule && r.Kind == kind }) {
			return rs, nil
		}
		return append(rs, core.Rule{Rule: rule, Kind: kind, Added: x.env.Now().UTC()}), nil
	})
	if err != nil {
		return err
	}
	x.RulesChanged()
	return nil
}

// RemoveRule drops a rule for all sessions: what it allowed asks again.
func (x *Runner) RemoveRule(rule string) error {
	err := x.rules.edit(func(rs []core.Rule) ([]core.Rule, error) {
		n := len(rs)
		rs = slices.DeleteFunc(rs, func(r core.Rule) bool { return r.Rule == rule })
		if len(rs) == n {
			return nil, fmt.Errorf("no rule %q for all sessions", rule)
		}
		return rs, nil
	})
	if err != nil {
		return err
	}
	x.RulesChanged()
	return nil
}

// allRule is the rule for all sessions that lets ask through, if one does.
func (x *Runner) allRule(ask core.Ask) (string, bool) {
	for _, r := range x.rules.all() {
		if r.Kind == ask.Kind && matchRule(r.Rule, ask) {
			return r.Rule, true
		}
	}
	return "", false
}
