package testrun

import (
	"regexp"
	"strconv"
	"strings"
)

// Want and got are pulled from failure output by the shapes common assertion styles print. The first match wins.
var (
	// Go: "got X, want Y", "want X, got Y", "f(x) = X, want Y", with or without colons.
	goGotWant  = regexp.MustCompile(`(?:^|[\s:(])got:?\s+(.+?),\s+want(?:ed)?:?\s+(.+?)\s*$`)
	goWantGot  = regexp.MustCompile(`(?:^|[\s:(])want(?:ed)?:?\s+(.+?),\s+got:?\s+(.+?)\s*$`)
	goEqWant   = regexp.MustCompile(`\)\s+=\s+(.+?),\s+want(?:ed)?:?\s+(.+?)\s*$`)
	expected   = regexp.MustCompile(`^\s*(?:Error:\s+)?(?:Expected|expected)\s*:\s?(.*)$`)
	actual     = regexp.MustCompile(`^\s*(?:Actual|actual|Received|received)\s*:\s?(.*)$`)
	chai       = regexp.MustCompile(`expected (.+?) to (?:be|equal|deeply equal|strictly equal|eql) (.+?)(?:\s+//.*)?$`)
	rustLeft   = regexp.MustCompile("^\\s*left:\\s*`?(.*?)`?,?$")
	rustRight  = regexp.MustCompile("^\\s*right:\\s*`?(.*?)`?$")
	pyAssertEq = regexp.MustCompile(`^(?:E\s+)?(?:AssertionError: )?assert (.+?) == (.+?)$`)
	goFileLine = regexp.MustCompile(`^\s+([\w.\-/]+\.go:\d+): (.*)$`)
)

// wantGot fills in f's Want and Got from output lines, when they show them.
func wantGot(f *Failure, lines []string) {
	if f.Want != "" || f.Got != "" {
		return
	}
	var exp, act, left, right string
	var haveExp, haveAct, haveLeft, haveRight bool
	for _, l := range lines {
		if m := goGotWant.FindStringSubmatch(l); m != nil {
			f.Got, f.Want = m[1], m[2]
			return
		}
		if m := goWantGot.FindStringSubmatch(l); m != nil {
			f.Want, f.Got = m[1], m[2]
			return
		}
		if m := goEqWant.FindStringSubmatch(l); m != nil {
			f.Got, f.Want = m[1], m[2]
			return
		}
		if m := pyAssertEq.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			f.Got, f.Want = m[1], m[2]
			return
		}
		if m := chai.FindStringSubmatch(l); m != nil && !strings.HasSuffix(m[1], " not") {
			f.Got, f.Want = m[1], m[2]
			return
		}
		if m := expected.FindStringSubmatch(l); m != nil && !haveExp {
			exp, haveExp = m[1], true
		} else if m := actual.FindStringSubmatch(l); m != nil && !haveAct {
			act, haveAct = m[1], true
		} else if m := rustLeft.FindStringSubmatch(l); m != nil && !haveLeft {
			left, haveLeft = m[1], true
		} else if m := rustRight.FindStringSubmatch(l); m != nil && !haveRight {
			right, haveRight = m[1], true
		}
	}
	switch {
	case haveExp && haveAct:
		f.Want, f.Got = strings.TrimSpace(exp), strings.TrimSpace(act)
	case haveLeft && haveRight:
		f.Got, f.Want = left, right
	}
}

// goLoc is the first "file.go:N: message" line of a Go test's output: where t.Error was called.
func goLoc(lines []string) (*Loc, string) {
	for _, l := range lines {
		if m := goFileLine.FindStringSubmatch(l); m != nil {
			loc, _ := fileLine(m[1])
			return loc, m[2]
		}
	}
	return nil, ""
}

// fileLine reads "path:line" or "path:line:col".
func fileLine(s string) (*Loc, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	for len(parts) > 1 {
		if _, err := strconv.Atoi(parts[len(parts)-1]); err != nil {
			break
		}
		if len(parts) >= 3 {
			if _, err := strconv.Atoi(parts[len(parts)-2]); err == nil {
				parts = parts[:len(parts)-1] // drop the column
				continue
			}
		}
		n, _ := strconv.Atoi(parts[len(parts)-1])
		return &Loc{File: strings.Join(parts[:len(parts)-1], ":"), Line: n - 1}, n > 0
	}
	return nil, false
}
