package vimregex

import "fmt"

// Error is a pattern that doesn't compile, or one too costly to match. ID names the problem and Args
// fill the blanks in its text.
type Error struct {
	ID   string
	Args []string
}

var errText = map[string]string{
	"too-many-groups":           "Too many groups: a pattern can have at most 9",
	"unmatched-open-noncapture": "Unmatched %s",
	"unmatched-open":            "Unmatched %s",
	"unmatched-close":           "Unmatched %s",
	"trailing":                  "Unexpected characters at the end of the pattern",
	"multi-after-multi":         "A repeat can't follow a repeat",
	"bad-brace":                 "Malformed %s",
	"premature-end":             "The pattern ends too early",
	"bad-class":                 "Unknown character class",
	"misplaced":                 "Misplaced %s",
	"repeat-zs":                 "%s can't be repeated",
	"z-group":                   `\z( isn't allowed here`,
	"z-ref":                     `\z1 to \z9 aren't allowed here`,
	"bad-backref":               "Back reference to a group that isn't there",
	"engine-not-first":          "%s must be at the start of the pattern",
	"bad-code-point":            "Invalid character after %s",
	"number-after-dot":          "No number allowed after the dot in %s",
	"missing-bracket":           "Missing ] after %s",
	"empty-optional":            "Empty %s",
	"unknown-lookaround":        "Unknown item %s",
	"unknown-z":                 "Unknown item %s",
	"unknown-percent":           "Unknown item %s",
	"too-complex":               "The pattern is too complex to match",
	"bad-engine":                `\%#= takes 0, 1 or 2`,
}

func (e *Error) Error() string {
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = a
	}
	return fmt.Sprintf(errText[e.ID], args...)
}
