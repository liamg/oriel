// Package changelog writes a release's notes from its commits' conventional titles: what a user would notice, under
// Breaking changes, Features and Fixes. Refactors, tests, CI, build, chores, docs and style are left out unless
// they break something, and so is a title that isn't conventional.
package changelog

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Commit is one commit: its title and the rest of its message.
type Commit struct {
	Subject string
	Body    string
}

// Entry is a commit as the notes list it.
type Entry struct {
	Type     string // feat, fix…
	Scope    string // lsp, ship… ; "" for none
	Breaking bool
	Text     string // the title with its type, scope and PR number taken off
	PR       int    // the squash merge's (#n); 0 for none
}

var (
	conventional = regexp.MustCompile(`^([a-z]+)(?:\(([^)]+)\))?(!)?:\s*(.+)$`)
	prSuffix     = regexp.MustCompile(`\s*\(#(\d+)\)$`)
	breakingNote = regexp.MustCompile(`(?m)^BREAKING[ -]CHANGE:`)
)

// Parse reads c's conventional title; ok is false for a title that isn't one.
func Parse(c Commit) (e Entry, ok bool) {
	m := conventional.FindStringSubmatch(strings.TrimSpace(c.Subject))
	if m == nil {
		return Entry{}, false
	}
	e = Entry{Type: m[1], Scope: m[2], Breaking: m[3] == "!" || breakingNote.MatchString(c.Body), Text: m[4]}
	if p := prSuffix.FindStringSubmatch(e.Text); p != nil {
		_, _ = fmt.Sscan(p[1], &e.PR)
		e.Text = strings.TrimSpace(prSuffix.ReplaceAllString(e.Text, ""))
	}
	return e, true
}

// section is where an entry goes: "" for one the notes leave out.
func section(e Entry) string {
	switch {
	case e.Breaking:
		return "Breaking changes"
	case e.Type == "feat":
		return "Features"
	case e.Type == "fix" || e.Type == "perf":
		return "Fixes"
	}
	return ""
}

var sections = []string{"Breaking changes", "Features", "Fixes"}

// NothingNotable is the notes of a release whose commits are all of the kinds left out.
const NothingNotable = "No notable changes."

// scopeNames are scopes whose plain capitalisation reads wrong.
var scopeNames = map[string]string{
	"lsp": "LSP", "dap": "Debugging", "mcp": "MCP", "ui": "UI", "ci": "CI", "github": "GitHub", "gitlab": "GitLab",
	"gitui": "Git", "lua": "Lua", "vim": "Vim", "plugin": "Plugins", "plugins": "Plugins", "review": "Reviews",
	"requests": "Requests", "sessions": "Sessions", "plan": "Plans", "tui": "TUI", "api": "API",
}

// ScopeName is how a scope reads in the notes: lsp is LSP, ship is Ship.
func ScopeName(scope string) string {
	if n, ok := scopeNames[scope]; ok {
		return n
	}
	if scope == "" {
		return ""
	}
	return strings.ToUpper(scope[:1]) + scope[1:]
}

// capitalised is s with its first letter a capital, as older titles began lower-case.
func capitalised(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// Notes is the markdown for commits, oldest first as git log --reverse gives them, with each PR number a link into
// repo (owner/name). A release with nothing a user would notice says so.
func Notes(commits []Commit, repo string) string {
	by := map[string][]Entry{}
	for _, c := range commits {
		if e, ok := Parse(c); ok {
			if s := section(e); s != "" {
				by[s] = append(by[s], e)
			}
		}
	}
	var b strings.Builder
	for _, s := range sections {
		if len(by[s]) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "## %s\n\n", s)
		for _, e := range by[s] {
			b.WriteString("- ")
			if e.Scope != "" {
				fmt.Fprintf(&b, "**%s:** ", ScopeName(e.Scope))
			}
			b.WriteString(capitalised(e.Text))
			if e.PR > 0 {
				fmt.Fprintf(&b, " ([#%d](https://github.com/%s/pull/%d))", e.PR, repo, e.PR)
			}
			b.WriteString("\n")
		}
	}
	if b.Len() == 0 {
		return NothingNotable + "\n"
	}
	return b.String()
}
