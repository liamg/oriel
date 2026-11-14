package vim

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/vim/vimtest/nvimtext"
)

func sample(k noticeKind) []any {
	args := []any{"1", "1", "1", "1", "1"}
	if k.textf == nil {
		args = args[:strings.Count(k.text, "%s")]
	}
	return args
}

// allKinds is every notice kind, by its var's name; TestNoticeKindsListed keeps it whole.
func allKinds() map[string]noticeKind {
	return map[string]noticeKind{
		"errNoFold":            errNoFold,
		"errFoldErase":         errFoldErase,
		"errFoldCreate":        errFoldCreate,
		"errFoldDelete":        errFoldDelete,
		"errLastWindow":        errLastWindow,
		"errNoAlternate":       errNoAlternate,
		"errRotateSplit":       errRotateSplit,
		"errRecursiveMapping":  errRecursiveMapping,
		"errRecursiveRegister": errRecursiveRegister,
		"errBufferNotFound":    errBufferNotFound,
		"errNoBuffer":          errNoBuffer,
		"errNoModified":        errNoModified,
		"errNoneDeleted":       errNoneDeleted,
		"errUnsaved":           errUnsaved,
		"errUnsavedBuffer":     errUnsavedBuffer,
		"errNoFileName":        errNoFileName,
		"errNoBufferMatch":     errNoBufferMatch,
		"errManyBufferMatch":   errManyBufferMatch,
		"errNoMapping":         errNoMapping,
		"errNoAbbrev":          errNoAbbrev,
		"errNoLocList":         errNoLocList,
		"errListEmpty":         errListEmpty,
		"errNoMoreItems":       errNoMoreItems,
		"errUnknownCommand":    errUnknownCommand,
		"errOldestList":        errOldestList,
		"errNewestList":        errNewestList,
		"errGrepDelim":         errGrepDelim,
		"errGrepArgs":          errGrepArgs,
		"errNoMatch":           errNoMatch,
		"errBadExpr":           errBadExpr,
		"errNoPrevPattern":     errNoPrevPattern,
		"errPatternNotFound":   errPatternNotFound,
		"errNoWord":            errNoWord,
		"errNoPrevRegister":    errNoPrevRegister,
		"errNoPrevCommand":     errNoPrevCommand,
		"errEmptyRegister":     errEmptyRegister,
		"errMarkNotSet":        errMarkNotSet,
		"errInvalidRange":      errInvalidRange,
		"errArgRequired":       errArgRequired,
		"errInvalidAddress":    errInvalidAddress,
		"errTrailing":          errTrailing,
		"errMoveIntoItself":    errMoveIntoItself,
		"errPositiveCount":     errPositiveCount,
		"errNumberRequired":    errNumberRequired,
		"errUnknownOption":     errUnknownOption,
		"errMissingDouble":     errMissingDouble,
		"errMissingSingle":     errMissingSingle,
		"errUndefinedVar":      errUndefinedVar,
		"errWriteNoName":       errWriteNoName,
		"errFileExists":        errFileExists,
		"errReadOnlyFile":      errReadOnlyFile,
		"errWriteFailed":       errWriteFailed,
		"errReadFailed":        errReadFailed,
		"errUnsavedFile":       errUnsavedFile,
		"errNoNameForBuffer":   errNoNameForBuffer,
		"msgOnlyWindow":        msgOnlyWindow,
		"msgReadOnlyFile":      msgReadOnlyFile,
		"msgWritten":           msgWritten,
		"askWriteChanged":      askWriteChanged,
		"msgNewestChange":      msgNewestChange,
		"msgOldestChange":      msgOldestChange,
		"msgFewerLines":        msgFewerLines,
		"msgMoreLines":         msgMoreLines,
		"msgYanked":            msgYanked,
		"msgMoved":             msgMoved,
		"msgEveryLine":         msgEveryLine,
		"msgNoLine":            msgNoLine,
		"msgNoLists":           msgNoLists,
		"msgQfEntry":           msgQfEntry,
		"msgShifted":           msgShifted,
		"msgMatchCount":        msgMatchCount,
		"msgSubstituted":       msgSubstituted,
		"msgQfList":            msgQfList,
		"msgBufferLine":        msgBufferLine,
		"msgFileInfo":          msgFileInfo,
		"askSwapRange":         askSwapRange,
	}
}

// Every notice kind declared in notice.go is in allKinds.
func TestNoticeKindsListed(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "notice.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := allKinds()
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR {
			continue
		}
		for _, sp := range g.Specs {
			for _, n := range sp.(*ast.ValueSpec).Names {
				if _, ok := listed[n.Name]; !ok {
					t.Errorf("%s isn't in allKinds", n.Name)
				}
			}
		}
	}
}

// Every notice has nvim's wording for the conformance cases, and none shows a vim error number.
func TestNotices(t *testing.T) {
	vimNumber := regexp.MustCompile(`\bE\d+:`)
	seen := map[string]bool{}
	for _, k := range allKinds() {
		n := k.with(sample(k)...)
		if seen[n.ID] {
			t.Errorf("%s: ID used twice", n.ID)
		}
		seen[n.ID] = true
		if strings.Contains(n.Text, "%!") || vimNumber.MatchString(n.Text) {
			t.Errorf("%s: text %q", n.ID, n.Text)
		}
		if got := nvimtext.Notice(n); strings.HasPrefix(got, "no nvim wording") || strings.Contains(got, "%!") {
			t.Errorf("%s: nvim wording %q", n.ID, got)
		}
	}
}
