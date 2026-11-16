package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
)

// Always holds what the user allowed for good from a card on this machine.
// It is kept apart from config.json, which the manager's settings push
// replaces, so a click on a card is not undone by the next save elsewhere.
type Always struct {
	Rules  []PolicyRule `json:"rules,omitempty"`
	Egress []EgressRule `json:"egress,omitempty"`
	// Shared are allowed everywhere: the manager collects them into the
	// settings every worker gets, then clears them here.
	Shared       []PolicyRule `json:"shared,omitempty"`
	SharedEgress []EgressRule `json:"sharedEgress,omitempty"`
	// NoSignIn are MCP servers the user will not sign in to on this machine.
	NoSignIn []string `json:"noSignIn,omitempty"`
}

var alwaysMu sync.Mutex

func alwaysPath() string { return filepath.Join(Base(), "always.json") }

// LoadAlways reads the machine's always-allow list; one it can't read allows nothing.
func LoadAlways() Always {
	a, _ := readAlways()
	return a
}

// readAlways reads the list: none yet is empty, and one that doesn't parse is an error, so it's never saved over.
func readAlways() (Always, error) {
	var a Always
	b, err := os.ReadFile(alwaysPath())
	if errors.Is(err, fs.ErrNotExist) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	if err := json.Unmarshal(b, &a); err != nil {
		return Always{}, fmt.Errorf("%s doesn't parse, so it's left as it is: fix or remove it: %w", alwaysPath(), err)
	}
	return a, nil
}

// UpdateAlways applies edit to the list and saves it. A list that doesn't parse is left alone, with an error.
func UpdateAlways(edit func(*Always)) error {
	alwaysMu.Lock()
	defer alwaysMu.Unlock()
	a, err := readAlways()
	if err != nil {
		return err
	}
	edit(&a)
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Base(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(alwaysPath(), b, 0o600)
}

// AddAlwaysRules records tool rules, replacing any for the same tool, and
// queues them for every machine when everywhere is set.
func AddAlwaysRules(rules []PolicyRule, everywhere bool) error {
	return UpdateAlways(func(a *Always) {
		for _, r := range rules {
			a.Rules = putRule(a.Rules, r)
			if everywhere {
				a.Shared = putRule(a.Shared, r)
			}
		}
	})
}

// AddAlwaysEgress records a network rule, likewise.
func AddAlwaysEgress(r EgressRule, everywhere bool) error {
	return UpdateAlways(func(a *Always) {
		a.Egress = PutEgress(a.Egress, r)
		if everywhere {
			a.SharedEgress = PutEgress(a.SharedEgress, r)
		}
	})
}

// ClearShared drops what the manager has taken into its settings.
func ClearShared(rules []PolicyRule, egress []EgressRule) error {
	return UpdateAlways(func(a *Always) {
		for _, r := range rules {
			a.Shared = slices.DeleteFunc(a.Shared, func(x PolicyRule) bool { return x == r })
		}
		for _, r := range egress {
			a.SharedEgress = slices.DeleteFunc(a.SharedEgress, func(x EgressRule) bool { return x == r })
		}
	})
}

// SetNoSignIn records, or forgets, that server is not to be signed in to here.
func SetNoSignIn(server string, ignore bool) error {
	return UpdateAlways(func(a *Always) {
		a.NoSignIn = slices.DeleteFunc(a.NoSignIn, func(x string) bool { return x == server })
		if ignore {
			a.NoSignIn = append(a.NoSignIn, server)
		}
	})
}

func putRule(rules []PolicyRule, r PolicyRule) []PolicyRule {
	rules = slices.DeleteFunc(rules, func(x PolicyRule) bool { return x.Tool == r.Tool })
	return append(rules, r)
}

// PutEgress adds r, replacing any rule for the same host and port.
func PutEgress(rules []EgressRule, r EgressRule) []EgressRule {
	rules = slices.DeleteFunc(rules, func(x EgressRule) bool { return x.Host == r.Host && x.Port == r.Port })
	return append(rules, r)
}

// PutRule adds r, replacing any rule for the same tool.
func PutRule(rules []PolicyRule, r PolicyRule) []PolicyRule {
	return putRule(rules, r)
}

// RemoveAlways drops a tool rule (tool set) or a network rule (host set).
func RemoveAlways(tool, host string, port int) error {
	return UpdateAlways(func(a *Always) {
		if tool != "" {
			a.Rules = slices.DeleteFunc(a.Rules, func(x PolicyRule) bool { return x.Tool == tool })
		}
		if host != "" {
			a.Egress = slices.DeleteFunc(a.Egress, func(x EgressRule) bool { return x.Host == host && x.Port == port })
		}
	})
}
