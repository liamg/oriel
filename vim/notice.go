package vim

import (
	"errors"
	"fmt"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/vim/vimregex"
)

// noticeKind is one notice the engine gives: its ID, severity and text, with %s for each argument,
// or a function when the text depends on them.
type noticeKind struct {
	id    string
	sev   edit.Severity
	text  string
	textf func(a []string) string
}

func (k noticeKind) with(args ...any) edit.Notice {
	a := make([]string, len(args))
	for i, x := range args {
		a[i] = fmt.Sprint(x)
	}
	n := edit.Notice{ID: k.id, Severity: k.sev, Args: a}
	if k.textf != nil {
		n.Text = k.textf(a)
	} else {
		n.Text = fmt.Sprintf(k.text, args...)
	}
	return n
}

// kinds is every notice kind, for tests.
var kinds []noticeKind

func kind(k noticeKind) noticeKind {
	kinds = append(kinds, k)
	return k
}

func errKind(id, text string) noticeKind {
	return kind(noticeKind{id: "vim." + id, sev: edit.Error, text: text})
}

func infoKind(id, text string) noticeKind {
	return kind(noticeKind{id: "vim." + id, sev: edit.Info, text: text})
}

func count(n, one, many string) string {
	if n == "1" {
		return n + " " + one
	}
	return n + " " + many
}

var (
	errNoFold           = errKind("no-fold", "No fold here")
	errFoldErase        = errKind("fold-erase-method", "Folds can only be erased when foldmethod is manual or marker")
	errFoldCreate       = errKind("fold-create-method", "Folds can only be created when foldmethod is manual or marker")
	errFoldDelete       = errKind("fold-delete-method", "Folds can only be deleted when foldmethod is manual or marker")
	errLastWindow       = errKind("last-window", "Can't close the last window")
	errNoAlternate      = errKind("no-alternate", "No alternate buffer")
	errRotateSplit      = errKind("rotate-split", "Can't rotate windows while one of them is split")
	errRecursiveMapping = errKind("recursive-mapping", "A mapping recursed too deeply")
	errBufferNotFound   = errKind("buffer-not-found", "Buffer %s not found")
	errNoBuffer         = errKind("no-buffer", "Buffer %s doesn't exist")
	errNoModified       = errKind("no-modified-buffer", "No buffer has unsaved changes")
	errNoneDeleted      = errKind("none-deleted", "No buffers were deleted")
	errUnsaved          = errKind("unsaved", "Unsaved changes (add ! to discard them)")
	errUnsavedBuffer    = errKind("unsaved-buffer", "Buffer %s has unsaved changes (add ! to discard them)")
	errNoFileName       = errKind("no-file-name", "The buffer has no file name")
	errNoBufferMatch    = errKind("no-buffer-match", "No buffer matches %s")
	errManyBufferMatch  = errKind("many-buffer-match", "More than one buffer matches %s")
	errNoMapping        = errKind("no-mapping", "No such mapping")
	errNoAbbrev         = errKind("no-abbrev", "No such abbreviation")
	errNoLocList        = errKind("no-loclist", "This window has no location list")
	errListEmpty        = errKind("list-empty", "The list is empty")
	errNoMoreItems      = errKind("no-more-items", "No more items")
	errUnknownCommand   = errKind("unknown-command", "Unknown command: %s")
	errOldestList       = errKind("oldest-list", "No older list")
	errNewestList       = errKind("newest-list", "No newer list")
	errGrepDelim        = errKind("grep-delimiter", "Invalid search pattern or delimiter")
	errGrepArgs         = errKind("grep-args", "Missing file name or invalid pattern")
	errNoMatch          = errKind("no-match", "No match: %s")
	errBadExpr          = errKind("bad-expression", "Invalid expression: %s")
	errNoPrevPattern    = errKind("no-previous-pattern", "No previous search pattern")
	errPatternNotFound  = errKind("pattern-not-found", "Pattern not found: %s")
	errNoWord           = errKind("no-word", "No word under the cursor")
	errNoPrevRegister   = errKind("no-previous-register", "No register has been run yet")
	errNoPrevCommand    = errKind("no-previous-command", "No previous command")
	errEmptyRegister    = errKind("empty-register", "Register %s is empty")
	errMarkNotSet       = errKind("mark-not-set", "Mark not set")
	errInvalidRange     = errKind("invalid-range", "Invalid range")
	errArgRequired      = errKind("argument-required", "This command needs an argument")
	errInvalidAddress   = errKind("invalid-address", "Invalid address")
	errTrailing         = errKind("trailing", "Unexpected characters: %s")
	errMoveIntoItself   = errKind("move-into-itself", "Can't move lines into themselves")
	errPositiveCount    = errKind("positive-count", "The count must be positive")
	errNumberRequired   = errKind("number-required", "Expected a number: %s")
	errUnknownOption    = errKind("unknown-option", "Unknown option: %s")
	errMissingDouble    = errKind("missing-double-quote", "Missing closing quote: %s")
	errMissingSingle    = errKind("missing-single-quote", "Missing closing quote: %s")
	errUndefinedVar     = errKind("undefined-variable", "Undefined variable: %s")

	msgOnlyWindow   = infoKind("only-window", "There's only one window")
	msgNewestChange = infoKind("newest-change", "Already at the newest change")
	msgOldestChange = infoKind("oldest-change", "Already at the oldest change")
	msgFewerLines   = infoKind("fewer-lines", "%s fewer lines")
	msgMoreLines    = infoKind("more-lines", "%s more lines")
	msgYanked       = infoKind("yanked", "%s lines yanked")
	msgMoved        = infoKind("moved", "%s lines moved")
	msgEveryLine    = infoKind("every-line", "The pattern matches every line: %s")
	msgNoLine       = infoKind("no-line", "The pattern matches no line: %s")
	msgNoLists      = infoKind("no-lists", "No lists yet")
	msgQfEntry      = infoKind("qf-entry", "(%s of %s): %s")
	msgShifted      = kind(noticeKind{id: "vim.shifted", sev: edit.Info, textf: func(a []string) string {
		verb := "Indented"
		if a[1] == "<" {
			verb = "Unindented"
		}
		s := verb + " " + count(a[0], "line", "lines")
		if a[2] != "1" {
			s += " " + a[2] + " times"
		}
		return s
	}})
	msgMatchCount = kind(noticeKind{id: "vim.match-count", sev: edit.Info, textf: func(a []string) string {
		return count(a[0], "match", "matches") + " on " + count(a[1], "line", "lines")
	}})
	msgSubstituted = kind(noticeKind{id: "vim.substituted", sev: edit.Info, textf: func(a []string) string {
		return count(a[0], "substitution", "substitutions") + " on " + count(a[1], "line", "lines")
	}})
	// msgQfList is one list of the quickfix stack: its mark in a history (">", " " or ""), number, how many,
	// size and title.
	msgQfList = kind(noticeKind{id: "vim.qf-list", sev: edit.Info, textf: func(a []string) string {
		s := "List " + a[1] + " of " + a[2] + ": " + count(a[3], "entry", "entries") + ", " + a[4]
		if a[0] == ">" {
			s = "> " + s
		}
		return s
	}})
	// msgBufferLine is a line of :ls: number, flags ("%a", "#h"…), modified ("1" or ""), name and line.
	msgBufferLine = kind(noticeKind{id: "vim.buffer-line", sev: edit.Info, textf: func(a []string) string {
		s := a[0] + " " + a[3]
		if a[2] == "1" {
			s += " (modified)"
		}
		return s + ", line " + a[4]
	}})
	// msgFileInfo is CTRL-G: name, modified, not edited ("1" or ""), lines, and how far through the cursor is.
	msgFileInfo = kind(noticeKind{id: "vim.file-info", sev: edit.Info, textf: func(a []string) string {
		s := a[0]
		if a[1] == "1" {
			s += " (modified)"
		}
		if a[3] == "0" {
			return s + ", empty"
		}
		return s + ", " + count(a[3], "line", "lines") + ", " + a[4] + "%"
	}})
	askSwapRange = kind(noticeKind{id: "vim.swap-range", sev: edit.Question, text: "The range is backwards. Swap it? (y/n)"})
)

func flag(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// asNotice turns an error into the notice that shows it.
func asNotice(err error) edit.Notice {
	var n edit.Notice
	if errors.As(err, &n) {
		return n
	}
	var re *vimregex.Error
	if errors.As(err, &re) {
		return edit.Notice{ID: "vim.regex." + re.ID, Severity: edit.Error, Args: re.Args, Text: re.Error()}
	}
	return edit.Notice{ID: "vim.error", Severity: edit.Error, Text: err.Error()}
}
