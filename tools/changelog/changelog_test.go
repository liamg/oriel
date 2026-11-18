package changelog

import (
	"strings"
	"testing"
)

// Titles from this repo's history, as their squash merges wrote them.
var history = []Commit{
	{Subject: "fix!: Every built-in command says who may run it, and the capabilities plugins consent to are enforced (#174)"},
	{Subject: "refactor!: Naming conventions, typed value sets and one shape per API (#175)"},
	{Subject: "refactor(host): The host holds its parts as named fields and writes out its API, the hostapi contracts; a part's own methods stay its own (#176)"},
	{Subject: "feat!: Commands declare their arguments, and a misspelled or mistyped key is an error (#178)"},
	{Subject: "feat(inbox): Mark all read: the cards the Inbox panel shows leave the stack and stop counting, and wait on to be answered (#180)"},
	{Subject: "test: End-to-end tests for behaviour no test reached, and the two bugs they found (#169)"},
	{Subject: "fix(lsp): Language servers stop when their session ends, and a killed one takes its helpers with it (#185)"},
	{Subject: "ci(release): Start a release by hand; tag only once everything has passed (#187)"},
	{Subject: "Revert \"test(isolate): macOS test files in a folder Spotlight doesn't index\""},
	{Subject: "chore(deps): bump something", Body: "BREAKING CHANGE: the minimum Go is 1.27"},
	{Subject: "perf(syntax): Highlighting a large file reparses only what changed"},
}

func TestParseReadsAConventionalTitle(t *testing.T) {
	for subject, want := range map[string]Entry{
		"fix(lsp): Language servers stop when their session ends (#185)": {Type: "fix", Scope: "lsp", Text: "Language servers stop when their session ends", PR: 185},
		"refactor!: Naming conventions (#175)":                           {Type: "refactor", Breaking: true, Text: "Naming conventions", PR: 175},
		"feat(plan): Start work lands on the work":                       {Type: "feat", Scope: "plan", Text: "Start work lands on the work"},
		"fix: errcheck 1825 → 0 (#167) (#168)":                           {Type: "fix", Text: "errcheck 1825 → 0 (#167)", PR: 168},
	} {
		got, ok := Parse(Commit{Subject: subject})
		if !ok || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", subject, got, ok, want)
		}
	}
	if _, ok := Parse(Commit{Subject: "Revert \"something\""}); ok {
		t.Error("a revert's title isn't conventional")
	}
}

// The notes keep what a user would notice: breaking changes first, then features and fixes. Refactors, tests and CI
// are left out unless they break something, and so is a title that isn't conventional.
func TestNotesListWhatAUserNotices(t *testing.T) {
	got := Notes(history, "liamg/oriel")
	want := `## Breaking changes

- Every built-in command says who may run it, and the capabilities plugins consent to are enforced ([#174](https://github.com/liamg/oriel/pull/174))
- Naming conventions, typed value sets and one shape per API ([#175](https://github.com/liamg/oriel/pull/175))
- Commands declare their arguments, and a misspelled or mistyped key is an error ([#178](https://github.com/liamg/oriel/pull/178))
- **Deps:** Bump something

## Features

- **Inbox:** Mark all read: the cards the Inbox panel shows leave the stack and stop counting, and wait on to be answered ([#180](https://github.com/liamg/oriel/pull/180))

## Fixes

- **LSP:** Language servers stop when their session ends, and a killed one takes its helpers with it ([#185](https://github.com/liamg/oriel/pull/185))
- **Syntax:** Highlighting a large file reparses only what changed
`
	if got != want {
		t.Errorf("notes:\n%s\nwant:\n%s", got, want)
	}
	for _, hidden := range []string{"#176", "#169", "#187", "Revert"} {
		if strings.Contains(got, hidden) {
			t.Errorf("the notes list %s, which a user wouldn't notice", hidden)
		}
	}
}

func TestNotesWithNothingToShowSaySo(t *testing.T) {
	got := Notes([]Commit{{Subject: "refactor: Long functions split (#162)"}, {Subject: "test: Flaky tests fixed (#184)"}}, "liamg/oriel")
	if got != "No notable changes.\n" {
		t.Errorf("notes = %q", got)
	}
}
