package script

import (
	"strconv"
	"strings"
)

// Frame is one line of a stack trace: a Go stack's or a Lua traceback's.
type Frame struct {
	Fn   string
	File string
	Line int
	Go   bool // a Go frame, behind "show Go frames" in a Lua trace
}

func (f Frame) Loc() string {
	if f.Line == 0 {
		return f.File
	}
	return f.File + ":" + strconv.Itoa(f.Line)
}

// ParseTrace reads the frames out of a Go stack (debug.Stack), a Lua traceback, or one followed by the other.
func ParseTrace(s string) []Frame {
	lines := strings.Split(s, "\n")
	var out []Frame
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		switch {
		case strings.HasPrefix(ln, "goroutine ") || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "stack traceback:"):
		case strings.HasPrefix(ln, "\t") && i > 0 && !strings.HasPrefix(lines[i-1], "\t") && isGoFn(lines[i-1]):
			// the file line under a Go function; read with it
		case !strings.HasPrefix(ln, "\t") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") && isGoFn(ln):
			file, line := SplitLoc(strings.Fields(strings.TrimSpace(lines[i+1]))[0])
			out = append(out, Frame{Fn: ln, File: file, Line: line, Go: true})
		case strings.HasPrefix(ln, "\t"):
			loc, rest, _ := strings.Cut(strings.TrimSpace(ln), ": ")
			file, line := SplitLoc(loc)
			out = append(out, Frame{Fn: rest, File: file, Line: line})
		}
	}
	return out
}

func isGoFn(s string) bool {
	return (strings.HasSuffix(s, ")") || strings.HasPrefix(s, "created by ")) && !strings.HasPrefix(s, "\t")
}

// SplitLoc is "file:12" as its file and line; a line of 0 when there's none.
func SplitLoc(s string) (string, int) {
	if i := strings.LastIndexByte(s, ':'); i > 0 {
		if n, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i], n
		}
	}
	return s, 0
}
