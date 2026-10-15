// Package autoapprove decides whether a pull request passes an auto-approve rule (crit's internal/autoapprove). It
// does no I/O: the caller gathers the PR's facts first.
package autoapprove

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Rule approves a review request whose PR passes When.
type Rule struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	When    Cond   `json:"when"`
}

// Cond is one node of a rule's tree: a group (All, Any) or one check. Exactly one field is set.
type Cond struct {
	All []Cond `json:"all,omitempty"`
	Any []Cond `json:"any,omitempty"`
	// Repo are regexes on "owner/name", anchored and case-insensitive.
	Repo []string `json:"repo,omitempty"`
	// Author are GitHub logins of the PR's author.
	Author []string `json:"author,omitempty"`
	// Requester are the agentbus users who sent the request.
	Requester []string `json:"requester,omitempty"`
	// Paths are globs every changed file must match one of.
	Paths []string `json:"paths,omitempty"`
	// MaxLines caps additions plus deletions.
	MaxLines *int `json:"maxLines,omitempty"`
	// ChecksGreen asks for every CI check to have passed.
	ChecksGreen bool `json:"checksGreen,omitempty"`
}

// Kind names the one field set, or "" when none or several are.
func (c Cond) Kind() string {
	var kinds []string
	if c.All != nil {
		kinds = append(kinds, "all")
	}
	if c.Any != nil {
		kinds = append(kinds, "any")
	}
	if c.Repo != nil {
		kinds = append(kinds, "repo")
	}
	if c.Author != nil {
		kinds = append(kinds, "author")
	}
	if c.Requester != nil {
		kinds = append(kinds, "requester")
	}
	if c.Paths != nil {
		kinds = append(kinds, "paths")
	}
	if c.MaxLines != nil {
		kinds = append(kinds, "maxLines")
	}
	if c.ChecksGreen {
		kinds = append(kinds, "checksGreen")
	}
	if len(kinds) != 1 {
		return ""
	}
	return kinds[0]
}

// PR is what the rules look at.
type PR struct {
	Repo        string // owner/name
	Author      string
	Requester   string
	Additions   int
	Deletions   int
	Files       []File
	ChecksGreen bool // every CI check passed
}

// File is one changed file; Previous is set on a rename.
type File struct {
	Path     string
	Previous string
}

// Parse reads rules written as JSON (a list of rules), and validates them; empty text is none.
func Parse(text string) ([]Rule, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	var rules []Rule
	if err := json.Unmarshal([]byte(text), &rules); err != nil {
		return nil, fmt.Errorf("auto-approve rules: %v", err)
	}
	return rules, Validate(rules)
}

// AnyEnabled says whether a rule could approve anything.
func AnyEnabled(rules []Rule) bool {
	for _, r := range rules {
		if r.Enabled {
			return true
		}
	}
	return false
}

// Match returns the first enabled rule pr passes.
func Match(rules []Rule, pr PR) (string, bool) {
	for _, r := range rules {
		if r.Enabled && eval(r.When, pr) {
			return r.Name, true
		}
	}
	return "", false
}

func eval(c Cond, pr PR) bool {
	switch c.Kind() {
	case "all":
		for _, k := range c.All {
			if !eval(k, pr) {
				return false
			}
		}
		return true
	case "any":
		for _, k := range c.Any {
			if eval(k, pr) {
				return true
			}
		}
		return false
	case "repo":
		for _, p := range c.Repo {
			if re, err := RepoRegexp(p); err == nil && re.MatchString(pr.Repo) {
				return true
			}
		}
		return false
	case "author":
		return anyFold(c.Author, pr.Author)
	case "requester":
		return anyFold(c.Requester, pr.Requester)
	case "paths":
		if len(pr.Files) == 0 {
			return false
		}
		for _, f := range pr.Files {
			if !matchAny(c.Paths, f.Path) || (f.Previous != "" && !matchAny(c.Paths, f.Previous)) {
				return false
			}
		}
		return true
	case "maxLines":
		return pr.Additions+pr.Deletions <= *c.MaxLines
	case "checksGreen":
		return pr.ChecksGreen
	}
	return false
}

func anyFold(list []string, s string) bool {
	if s == "" {
		return false
	}
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), s) {
			return true
		}
	}
	return false
}

func matchAny(globs []string, file string) bool {
	for _, g := range globs {
		if Glob(g, file) {
			return true
		}
	}
	return false
}

// Glob matches a repo-relative file against a pattern. A pattern with no "/" matches the file's name in any
// directory; "**" matches any number of directories.
func Glob(pattern, file string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "/")
	if !strings.Contains(pattern, "/") {
		ok, _ := path.Match(pattern, path.Base(file))
		return ok
	}
	return globParts(strings.Split(pattern, "/"), strings.Split(file, "/"))
}

func globParts(pat, parts []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(parts); i++ {
				if globParts(pat[1:], parts[i:]) {
					return true
				}
			}
			return false
		}
		if len(parts) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], parts[0]); !ok {
			return false
		}
		pat, parts = pat[1:], parts[1:]
	}
	return len(parts) == 0
}

// RepoRegexp compiles a repo pattern the way rules match it.
func RepoRegexp(p string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)^(?:" + p + ")$")
}

// Validate refuses a rule that's malformed, or that could pass without a paths or maxLines check.
func Validate(rules []Rule) error {
	for i, r := range rules {
		name := r.Name
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("auto-approve rule %d has no name", i+1)
		}
		limit, err := checkCond(r.When, "when")
		if err != nil {
			return fmt.Errorf("auto-approve rule %q: %v", name, err)
		}
		if !limit {
			return fmt.Errorf("auto-approve rule %q: every way it can pass needs a paths or maxLines check", name)
		}
	}
	return nil
}

// checkCond reports whether every passing path through c includes a paths or maxLines check.
func checkCond(c Cond, at string) (limit bool, err error) {
	kind := c.Kind()
	switch kind {
	case "":
		return false, fmt.Errorf("%s: set exactly one of all, any, repo, author, requester, paths, maxLines, checksGreen", at)
	case "all", "any":
		kids := c.All
		if kind == "any" {
			kids = c.Any
		}
		if len(kids) == 0 {
			return false, fmt.Errorf("%s: empty %s group", at, kind)
		}
		limit = kind == "any"
		for i, k := range kids {
			l, err := checkCond(k, fmt.Sprintf("%s.%s[%d]", at, kind, i))
			if err != nil {
				return false, err
			}
			if kind == "all" {
				limit = limit || l
			} else {
				limit = limit && l
			}
		}
		return limit, nil
	case "repo":
		if err := nonEmpty(c.Repo, at, kind); err != nil {
			return false, err
		}
		for _, p := range c.Repo {
			if _, err := RepoRegexp(p); err != nil {
				return false, fmt.Errorf("%s: bad repo pattern %q: %v", at, p, err)
			}
		}
		return false, nil
	case "paths":
		if err := nonEmpty(c.Paths, at, kind); err != nil {
			return false, err
		}
		for _, p := range c.Paths {
			for _, part := range strings.Split(p, "/") {
				if part == "**" {
					continue
				}
				if _, err := path.Match(part, ""); err != nil {
					return false, fmt.Errorf("%s: bad path glob %q", at, p)
				}
			}
		}
		return true, nil
	case "maxLines":
		if *c.MaxLines < 0 {
			return false, fmt.Errorf("%s: maxLines must not be negative", at)
		}
		return true, nil
	case "author":
		return false, nonEmpty(c.Author, at, kind)
	case "requester":
		return false, nonEmpty(c.Requester, at, kind)
	}
	return false, nil
}

func nonEmpty(list []string, at, kind string) error {
	for _, s := range list {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s: empty %s entry", at, kind)
		}
	}
	if len(list) == 0 {
		return errors.New(at + ": " + kind + " needs at least one entry")
	}
	return nil
}
