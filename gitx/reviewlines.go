package gitx

import (
	"cmp"
	"context"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// PatchLines are the lines of one file's patch a review comment can sit on, by side: RIGHT the new file's, LEFT the
// old's.
func PatchLines(patch string) map[string]map[int]bool {
	out := map[string]map[int]bool{"RIGHT": {}, "LEFT": {}}
	old, nw := 0, 0
	for _, ln := range strings.Split(patch, "\n") {
		if m := hunkHeader.FindStringSubmatch(ln); m != nil {
			old, _ = strconv.Atoi(m[1])
			nw, _ = strconv.Atoi(m[2])
			continue
		}
		switch {
		case nw == 0 && old == 0, ln == "", strings.HasPrefix(ln, "\\"):
		case strings.HasPrefix(ln, "+"):
			out["RIGHT"][nw] = true
			nw++
		case strings.HasPrefix(ln, "-"):
			out["LEFT"][old] = true
			old++
		default:
			out["RIGHT"][nw], out["LEFT"][old] = true, true
			nw++
			old++
		}
	}
	return out
}

// FilePatches splits a diff of many files as git diff writes it into each file's, by its path: the new one, or the
// old one for a deleted file.
func FilePatches(diff string) map[string]string {
	out := map[string]string{}
	var path, old string
	var body strings.Builder
	flush := func() {
		if p := cmp.Or(path, old); p != "" {
			out[p] = body.String()
		}
		path, old = "", ""
		body.Reset()
	}
	inHeader := false
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			flush()
			inHeader = true
			continue
		case inHeader && strings.HasPrefix(ln, "--- "):
			old = patchPath(ln[4:], "a/")
			continue
		case inHeader && strings.HasPrefix(ln, "+++ "):
			path = patchPath(ln[4:], "b/")
			continue
		case strings.HasPrefix(ln, "@@"):
			inHeader = false
		case inHeader:
			continue
		}
		body.WriteString(ln + "\n")
	}
	flush()
	return out
}

func patchPath(s, prefix string) string {
	s = strings.TrimRight(s, "\t")
	if u, err := strconv.Unquote(s); err == nil {
		s = u
	}
	if s == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(s, prefix)
}

// ReviewLines are the lines a pull request review comment can sit on in the change from where HEAD left base
// (BaseRef's when base is "") to HEAD: by file, then side as PatchLines has them. A file missing from it isn't in
// the change.
func (r Repo) ReviewLines(ctx context.Context, base string) (map[string]map[string]map[int]bool, error) {
	diff, err := r.Diff(ctx, r.ForkPoint(ctx, base), "HEAD")
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]map[int]bool{}
	for path, patch := range FilePatches(diff) {
		out[path] = PatchLines(patch)
	}
	return out, nil
}
