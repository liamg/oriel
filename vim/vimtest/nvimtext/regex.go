// Package nvimtext words the engine's notices as nvim does, so tests can check them against nvim's
// messages. Only tests use it: Oriel shows its own wording.
package nvimtext

import "fmt"

// Regex is a vimregex error, by its ID and arguments, as nvim words it.
func Regex(id string, args []string) string {
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	unknown := func(code string) string {
		if len(arg(0)) == 2 {
			return fmt.Sprintf("%s: (NFA) Unknown operator '%s", code, arg(0))
		}
		return fmt.Sprintf("%s: (NFA) Unknown operator '%s'", code, arg(0))
	}
	switch id {
	case "too-many-groups":
		return "E872: (NFA regexp) Too many '('"
	case "unmatched-open-noncapture":
		return "E53: Unmatched " + arg(0)
	case "unmatched-open":
		return "E54: Unmatched " + arg(0)
	case "unmatched-close":
		return "E55: Unmatched " + arg(0)
	case "trailing":
		return "E488: Trailing characters"
	case "multi-after-multi":
		return "E871: (NFA regexp) Can't have a multi follow a multi"
	case "bad-brace":
		return "E554: Syntax error in " + arg(0)
	case "premature-end":
		return "E865: (NFA) Regexp end encountered prematurely"
	case "bad-class":
		return "E877: (NFA regexp) Invalid character class: " + arg(0)
	case "misplaced":
		return "E866: (NFA regexp) Misplaced " + arg(0)
	case "repeat-zs":
		return "E888: (NFA regexp) cannot repeat " + arg(0)
	case "z-group":
		return `E66: \z( not allowed here`
	case "z-ref":
		return `E67: \z1 - \z9 not allowed here`
	case "bad-backref":
		return "E65: Illegal back reference"
	case "engine-not-first":
		return fmt.Sprintf("E1281: Atom '%s' must be at the start of the pattern", arg(0))
	case "bad-code-point":
		return "E678: Invalid character after " + arg(0)
	case "number-after-dot":
		return fmt.Sprintf("E1204: No Number allowed after .: '%s'", arg(0))
	case "missing-bracket":
		return "E69: Missing ] after " + arg(0)
	case "empty-optional":
		return "E70: Empty " + arg(0)
	case "unknown-lookaround":
		return unknown("E869")
	case "unknown-z", "unknown-percent":
		return unknown("E867")
	case "too-complex":
		return "E363: pattern uses more memory than 'maxmempattern'"
	case "reverse-range":
		return "E944: Reverse range in character class"
	case "bad-engine":
		return `E864: \%#= can only be followed by 0, 1, or 2. The automatic engine will be used `
	}
	return "no nvim wording for regex " + id
}
