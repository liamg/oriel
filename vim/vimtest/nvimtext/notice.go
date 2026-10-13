package nvimtext

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/liamg/oriel/edit"
)

// fixed is each notice whose nvim wording is a format of its arguments.
var fixed = map[string]string{
	"vim.no-fold":              "E490: No fold found",
	"vim.fold-erase-method":    "E352: Cannot erase folds with current 'foldmethod'",
	"vim.fold-create-method":   "E350: Cannot create fold with current 'foldmethod'",
	"vim.fold-delete-method":   "E351: Cannot delete fold with current 'foldmethod'",
	"vim.last-window":          "E444: Cannot close last window",
	"vim.no-alternate":         "E23: No alternate file",
	"vim.rotate-split":         "E443: Cannot rotate when another window is split",
	"vim.recursive-mapping":    "E223: Recursive mapping",
	"vim.buffer-not-found":     "E92: Buffer %s not found",
	"vim.no-buffer":            "E86: Buffer %s does not exist",
	"vim.no-modified-buffer":   "E84: No modified buffer found",
	"vim.none-deleted":         "E516: No buffers were deleted",
	"vim.unsaved":              "E37: No write since last change (add ! to override)",
	"vim.unsaved-buffer":       "E89: No write since last change for buffer %s (add ! to override)",
	"vim.no-file-name":         `E499: Empty file name for '%%' or '#', only works with ":p:h"`,
	"vim.no-buffer-match":      "E94: No matching buffer for %s",
	"vim.many-buffer-match":    "E93: More than one match for %s",
	"vim.no-mapping":           "E31: No such mapping",
	"vim.no-abbrev":            "E24: No such abbreviation",
	"vim.no-loclist":           "E776: No location list",
	"vim.list-empty":           "E42: No Errors",
	"vim.no-more-items":        "E553: No more items",
	"vim.unknown-command":      "E492: Not an editor command: %s",
	"vim.oldest-list":          "E380: At bottom of quickfix stack",
	"vim.newest-list":          "E381: At top of quickfix stack",
	"vim.grep-delimiter":       "E682: Invalid search pattern or delimiter",
	"vim.grep-args":            "E683: File name missing or invalid pattern",
	"vim.no-match":             "E480: No match: %s",
	"vim.bad-expression":       "E15: Invalid expression: %s",
	"vim.no-previous-pattern":  "E35: No previous regular expression",
	"vim.pattern-not-found":    "E486: Pattern not found: %s",
	"vim.no-word":              "E348: No string under cursor",
	"vim.no-previous-register": "E748: No previously used register",
	"vim.no-previous-command":  "E30: No previous command line",
	"vim.empty-register":       "E353: Nothing in register %s",
	"vim.mark-not-set":         "E20: Mark not set",
	"vim.invalid-range":        "E16: Invalid range",
	"vim.argument-required":    "E471: Argument required",
	"vim.invalid-address":      "E14: Invalid address",
	"vim.trailing":             "E488: Trailing characters: %s",
	"vim.move-into-itself":     "E134: Cannot move a range of lines into itself",
	"vim.positive-count":       "E939: Positive count required",
	"vim.number-required":      "E521: Number required after =: %s",
	"vim.unknown-option":       "E518: Unknown option: %s",
	"vim.missing-double-quote": "E114: Missing double quote: %s",
	"vim.missing-single-quote": "E115: Missing single quote: %s",
	"vim.undefined-variable":   "E121: Undefined variable: %s",
	"vim.only-window":          "Already only one window",
	"vim.newest-change":        "Already at newest change",
	"vim.oldest-change":        "Already at oldest change",
	"vim.fewer-lines":          "%s fewer lines",
	"vim.more-lines":           "%s more lines",
	"vim.yanked":               "%s lines yanked",
	"vim.moved":                "%s lines moved",
	"vim.every-line":           "Pattern found in every line: %s",
	"vim.no-line":              "Pattern not found: %s",
	"vim.no-lists":             "No entries",
	"vim.qf-entry":             "(%s of %s): %s",
	"vim.swap-range":           "Backwards range given, OK to swap (y/n)?",
}

func plural(n, one, many string) string {
	if n == "1" {
		return n + " " + one
	}
	return n + " " + many
}

// Notice is a notice as nvim words it. The zero notice is no message.
func Notice(n edit.Notice) string {
	if n.ID == "" {
		return ""
	}
	a := n.Args
	if id, ok := strings.CutPrefix(n.ID, "vim.regex."); ok {
		return Regex(id, a)
	}
	if f, ok := fixed[n.ID]; ok {
		args := make([]any, len(a))
		for i, x := range a {
			args[i] = x
		}
		return fmt.Sprintf(f, args...)
	}
	switch n.ID {
	case "vim.shifted":
		return fmt.Sprintf("%s lines %sed %s", a[0], a[1], plural(a[2], "time", "times"))
	case "vim.match-count":
		return plural(a[0], "match", "matches") + " on " + plural(a[1], "line", "lines")
	case "vim.substituted":
		return plural(a[0], "substitution", "substitutions") + " on " + plural(a[1], "line", "lines")
	case "vim.qf-list":
		lead := ""
		if a[0] != "" {
			lead = a[0] + " "
		}
		m := fmt.Sprintf("%serror list %s of %s; %s errors ", lead, a[1], a[2], a[3])
		if len(m) < 34 {
			m += strings.Repeat(" ", 34-len(m))
		}
		return m + a[4]
	case "vim.buffer-line":
		mod := " "
		if a[2] == "1" {
			mod = "+"
		}
		num, _ := strconv.Atoi(a[0])
		return fmt.Sprintf("%3d %s %s %-30s line %s", num, a[1], mod, `"`+a[3]+`"`, a[4])
	case "vim.file-info":
		var flags string
		if a[1] == "1" {
			flags += "[Modified]"
		}
		if a[2] == "1" {
			flags += "[Not edited]"
		}
		if flags != "" {
			flags = " " + flags
		}
		if a[3] == "0" {
			return fmt.Sprintf(`"%s"%s --No lines in buffer--`, a[0], flags)
		}
		return fmt.Sprintf(`"%s"%s %s --%s%%--`, a[0], flags, plural(a[3], "line", "lines"), a[4])
	case "vim.error":
		return n.Text
	}
	return "no nvim wording for " + n.ID
}
