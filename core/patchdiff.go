package core

import (
	"strconv"
	"strings"
)

// PatchDiff reads one file's unified diff as a FileDiff. Header lines are skipped.
func PatchDiff(path, patch string) *FileDiff {
	d := &FileDiff{Path: path}
	if patch == "" {
		return d
	}
	oldN, newN, inHunk := 0, 0, false
	for _, l := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
		switch {
		case l == "":
		case strings.HasPrefix(l, "@@"):
			oldN, newN = hunkStart(l)
			inHunk = true
		case !inHunk && (strings.HasPrefix(l, "---") || strings.HasPrefix(l, "+++")), strings.HasPrefix(l, `\`):
			// the file headers, before the first hunk; a removed "-- x" line inside one is a line
		case strings.HasPrefix(l, "+"):
			d.Lines = append(d.Lines, DiffLine{Op: "+", New: newN, Text: l[1:]})
			d.Added++
			newN++
		case strings.HasPrefix(l, "-"):
			d.Lines = append(d.Lines, DiffLine{Op: "-", Old: oldN, Text: l[1:]})
			d.Removed++
			oldN++
		default:
			d.Lines = append(d.Lines, DiffLine{Op: " ", Old: oldN, New: newN, Text: strings.TrimPrefix(l, " ")})
			oldN++
			newN++
		}
	}
	return d
}

// hunkStart reads "@@ -a,b +c,d @@" as a and c.
func hunkStart(h string) (int, int) {
	var oldN, newN int
	for _, f := range strings.Fields(h) {
		if len(f) < 2 {
			continue
		}
		n, _, _ := strings.Cut(f[1:], ",")
		v, err := strconv.Atoi(n)
		if err != nil {
			continue
		}
		switch f[0] {
		case '-':
			oldN = v
		case '+':
			newN = v
		}
	}
	return oldN, newN
}

// ShellTool says a tool runs shell commands, by the names the agents give it.
func ShellTool(tool string) bool {
	switch tool {
	case "Bash", "shell", "commandExecution", "exec_command", "local_shell":
		return true
	}
	return false
}
