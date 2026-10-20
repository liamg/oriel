package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/liamg/oriel/gitx"
)

// walkFormat is the prompt for how a walkthrough's answer is written (ExplainRequest.Format): JSON steps in one fenced block.
const walkFormat = "explain.walkthrough"

// walkStep is a step of a walkthrough: a title, its prose, the lines of a file it's about and notes on parts of them.
type walkStep struct {
	Title, Prose  string
	Project, File string // as the answer names them
	Start, End    int    // 1-based lines; 0 for a step with no code (the summary)
	Notes         []walkNote

	path     string // the file, absolute; "" for a step with no code
	proj     string // its project's name
	rel      string // the file in its project, or what the answer named when it isn't found
	missing  bool   // it names a file that isn't there
	from, to int    // the lines it's kept for: its own and its notes'
	hash     string // their text when it was written
}

// walkNote is a note pinned to lines of its step's file, Mark the text on them to highlight.
type walkNote struct {
	Title, Text, Mark string
	Start, End        int
}

// walkLines is a span of lines as an answer may write it: [107, 111], [107], 107, "107-111" or "L107–111".
type walkLines struct{ a, b int }

var digits = regexp.MustCompile(`\d+`)

func (l *walkLines) UnmarshalJSON(b []byte) error {
	var nums []int
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case float64:
		nums = []int{int(v)}
	case string:
		for _, d := range digits.FindAllString(v, 2) {
			n, _ := strconv.Atoi(d)
			nums = append(nums, n)
		}
	case []any:
		for _, x := range v {
			if f, ok := x.(float64); ok {
				nums = append(nums, int(f))
			}
		}
	}
	if len(nums) > 0 {
		l.a, l.b = nums[0], nums[len(nums)-1]
	}
	return nil
}

// span is the lines in order, from start and end when lines didn't give them.
func (l walkLines) span(start, end int) (int, int) {
	a, b := l.a, l.b
	if a == 0 {
		a, b = start, end
	}
	if b == 0 {
		b = a
	}
	if b < a {
		a, b = b, a
	}
	return a, b
}

type rawNote struct {
	Title string    `json:"title"`
	Lines walkLines `json:"lines"`
	Start int       `json:"start"`
	End   int       `json:"end"`
	Mark  string    `json:"mark"`
	Text  string    `json:"text"`
	Body  string    `json:"body"`
}

type rawStep struct {
	Title   string    `json:"title"`
	Project string    `json:"project"`
	File    string    `json:"file"`
	Lines   walkLines `json:"lines"`
	Start   int       `json:"start"`
	End     int       `json:"end"`
	Prose   string    `json:"prose"`
	Text    string    `json:"text"`
	Notes   []rawNote `json:"notes"`
}

var (
	fenced        = regexp.MustCompile("(?s)```[A-Za-z]*[ \t]*\r?\n(.*?)```")
	trailingComma = regexp.MustCompile(`,(\s*[}\]])`)
)

// parseWalkthrough reads the steps out of an answer: the JSON in its first fenced block that has them, or the answer
// itself, or the object or list inside it; a trailing comma is forgiven.
func parseWalkthrough(answer string) ([]walkStep, error) {
	var cands []string
	for _, m := range fenced.FindAllStringSubmatch(answer, -1) {
		cands = append(cands, m[1])
	}
	cands = append(cands, answer)
	for _, open := range []string{"{", "["} {
		closer := map[string]string{"{": "}", "[": "]"}[open]
		if i, j := strings.Index(answer, open), strings.LastIndex(answer, closer); i >= 0 && j > i {
			cands = append(cands, answer[i:j+1])
		}
	}
	for _, c := range cands {
		for _, src := range []string{c, trailingComma.ReplaceAllString(c, "$1")} {
			if steps := decodeSteps(strings.TrimSpace(src)); len(steps) > 0 {
				return steps, nil
			}
		}
	}
	start := []rune(firstLine(strings.TrimSpace(answer)))
	if len(start) > 60 {
		start = append(start[:60], '…')
	}
	return nil, fmt.Errorf("the answer has no walkthrough in it (its steps should be JSON in a ```json block); it began %q", string(start))
}

func decodeSteps(src string) []walkStep {
	var raw []rawStep
	var obj struct {
		Steps       []rawStep `json:"steps"`
		Walkthrough []rawStep `json:"walkthrough"`
	}
	if json.Unmarshal([]byte(src), &obj) == nil {
		raw = append(obj.Steps, obj.Walkthrough...)
	} else if json.Unmarshal([]byte(src), &raw) != nil {
		return nil
	}
	var out []walkStep
	for _, r := range raw {
		s := walkStep{Title: strings.TrimSpace(r.Title), Prose: strings.TrimSpace(cmpOr(r.Prose, r.Text)), Project: strings.TrimSpace(r.Project),
			File: strings.TrimSpace(strings.TrimPrefix(r.File, "./"))}
		if s.Title == "" && s.Prose == "" {
			continue
		}
		s.Start, s.End = r.Lines.span(r.Start, r.End)
		for _, n := range r.Notes {
			wn := walkNote{Title: strings.TrimSpace(n.Title), Text: strings.TrimSpace(cmpOr(n.Text, n.Body)), Mark: n.Mark}
			if wn.Title == "" && wn.Text == "" {
				continue
			}
			wn.Start, wn.End = n.Lines.span(n.Start, n.End)
			s.Notes = append(s.Notes, wn)
		}
		out = append(out, s)
	}
	return out
}

// walkProject is a project the change is in: its name and directory.
type walkProject struct{ name, dir string }

// walkFile is a file the branch changed.
type walkFile struct {
	project, dir string
	rel          string // in its project, with slashes
	path         string
	code         byte   // git's status letter
	src          []byte // as it is now: an open buffer's text, else the file's
	diff         string
	first, last  int // its first and last changed lines, 1-based
}

// walkChange is the branch's change in every project.
type walkChange struct {
	projects []walkProject
	files    []*walkFile
	skipped  []string // projects with nothing to read: not a repository
}

// openText is an open buffer's text, and as it would be written.
type openText struct{ src, file []byte }

// gatherChange reads every project's changed and untracked files against its branch's base (else HEAD), with open
// buffers' text standing for their files.
func gatherChange(ctx context.Context, projects []walkProject, open map[string]openText) (*walkChange, error) {
	ch := &walkChange{projects: projects}
	seen := map[string]bool{}
	for _, p := range projects {
		root, ok := gitx.Root(ctx, p.dir)
		if !ok {
			ch.skipped = append(ch.skipped, p.name)
			continue
		}
		repo := gitx.Repo{Dir: root}
		st, err := repo.Status(ctx)
		if err != nil {
			return nil, err
		}
		files := st.Files(gitx.Changed)
		for _, f := range st.Files(gitx.Uncommitted) {
			if f.Area == gitx.Untracked {
				files = append(files, f)
			}
		}
		for _, f := range files {
			if strings.HasSuffix(f.Path, "/") || f.Binary {
				continue
			}
			abs := filepath.Join(root, filepath.FromSlash(f.Path))
			if seen[abs] {
				continue
			}
			seen[abs] = true
			rel, err := filepath.Rel(p.dir, abs)
			if err != nil || !filepath.IsLocal(rel) {
				rel = f.Path
			}
			wf := &walkFile{project: p.name, dir: p.dir, rel: filepath.ToSlash(rel), path: abs, code: f.Code}
			file := []byte(nil)
			if o, ok := open[filepath.Clean(abs)]; ok {
				wf.src, file = o.src, o.file
			} else if f.Code != 'D' {
				wf.src, _ = os.ReadFile(abs)
				file = wf.src
			}
			old := cmpOr(f.Old, f.Path)
			var base []byte
			if st.BaseHash != "" {
				base, _ = repo.FileAt(ctx, st.BaseHash, old)
			} else {
				base, _ = repo.HeadFile(ctx, old)
			}
			wf.diff, wf.first, wf.last = numberedDiff(base, file)
			ch.files = append(ch.files, wf)
		}
	}
	if len(ch.files) == 0 {
		return nil, errors.New("the branch changes nothing to explain yet")
	}
	return ch, nil
}

// maxDiffLines is how much of one file's diff the explainer is shown.
const maxDiffLines = 800

// numberedDiff is base→file as the explainer reads it: each new or kept line with its number in the file now, a
// removed one without, and three lines around each change; and the first and last lines changed.
func numberedDiff(base, file []byte) (out string, first, last int) {
	lines := gitx.SplitLines(file)
	hunks := gitx.DiffLines(base, file)
	changed := map[int]bool{}
	removed := map[int][]string{} // before line n
	for _, h := range hunks {
		at := h.NewStart
		if h.NewLines == 0 {
			at = h.NewStart + 1
		}
		removed[at] = append(removed[at], h.Old...)
		for i := range h.NewLines {
			changed[h.NewStart+i] = true
		}
		a, z := at, max(at, h.NewStart+h.NewLines-1)
		if first == 0 || a < first {
			first = a
		}
		last = max(last, min(z, max(len(lines), 1)))
	}
	show := func(n int) bool {
		for d := -3; d <= 3; d++ {
			if changed[n+d] || len(removed[n+d]) > 0 && d > -3 {
				return true
			}
		}
		return false
	}
	var b strings.Builder
	n, gap := 0, false
	write := func(s string) {
		if n < maxDiffLines {
			b.WriteString(s)
		}
		n++
	}
	for i := 1; i <= len(lines)+1; i++ {
		for _, r := range removed[i] {
			write(fmt.Sprintf("-      | %s\n", strings.TrimRight(r, "\r\n")))
		}
		if i > len(lines) {
			break
		}
		if !show(i) {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			write("  ⋯\n")
		}
		gap = false
		mark := " "
		if changed[i] {
			mark = "+"
		}
		write(fmt.Sprintf("%s %4d | %s\n", mark, i, strings.TrimRight(lines[i-1], "\r\n")))
	}
	if n > maxDiffLines {
		fmt.Fprintf(&b, "  … %d more lines\n", n-maxDiffLines)
	}
	return b.String(), first, last
}

// subject is the change in a line: "6 files in 2 projects".
func (ch *walkChange) subject() string {
	s := fmt.Sprintf("%d %s", len(ch.files), plural(len(ch.files), "file", "files"))
	if n := ch.projectCount(); n > 1 {
		s += fmt.Sprintf(" in %d projects", n)
	}
	return s
}

// projectCount is how many projects the changed files are in.
func (ch *walkChange) projectCount() int {
	seen := map[string]bool{}
	for _, f := range ch.files {
		seen[f.project] = true
	}
	return len(seen)
}

// context is what the explainer is shown: every changed file's diff, numbered.
func (ch *walkChange) context() string {
	var b strings.Builder
	b.WriteString("The branch's change, against where it left its base. A line starting + is new or changed, one starting - " +
		"was removed, and the numbers are each file's line numbers as it is now.\n")
	for _, f := range ch.files {
		what := map[byte]string{'A': "added", 'D': "deleted", 'R': "renamed", '?': "added"}[f.code]
		fmt.Fprintf(&b, "\n## project %s · %s (%s)\n```diff\n%s```\n", f.project, f.rel, cmpOr(what, "modified"), f.diff)
	}
	return b.String()
}

// resolve finds each step's file in the change (else in a project, as it is on disk), keeps its lines and notes in
// the file, and hashes the lines it's about.
func (ch *walkChange) resolve(steps []walkStep) []walkStep {
	for i := range steps {
		s := &steps[i]
		if s.File == "" {
			s.Start, s.End, s.Notes = 0, 0, nil
			continue
		}
		f := ch.find(s.Project, s.File)
		if f == nil || f.src == nil && f.code == 'D' {
			s.rel, s.proj, s.missing = s.File, s.Project, true
			s.Start, s.End, s.Notes = 0, 0, nil
			continue
		}
		s.path, s.proj, s.rel = f.path, f.project, f.rel
		n := len(fileLines(f.src))
		if s.Start == 0 {
			s.Start, s.End = max(f.first, 1), max(f.last, 1)
		}
		s.Start, s.End = clampLine(s.Start, n), clampLine(s.End, n)
		s.from, s.to = s.Start, s.End
		for j := range s.Notes {
			nt := &s.Notes[j]
			if nt.Start == 0 {
				nt.Start, nt.End = s.Start, s.End
			}
			nt.Start, nt.End = clampLine(nt.Start, n), clampLine(nt.End, n)
			s.from, s.to = min(s.from, nt.Start), max(s.to, nt.End)
		}
		s.hash = linesHash(f.src, s.from, s.to)
	}
	return steps
}

// find is a step's file: the changed file it names, by path in its project or by the end of its path; else one in a
// project as it is on disk.
func (ch *walkChange) find(project, file string) *walkFile {
	file = filepath.ToSlash(file)
	match := func(f *walkFile) bool { return project == "" || f.project == project }
	for _, f := range ch.files {
		if match(f) && f.rel == file {
			return f
		}
	}
	for _, f := range ch.files {
		if match(f) && (strings.HasSuffix(f.rel, "/"+file) || strings.HasSuffix(file, "/"+f.rel)) {
			return f
		}
	}
	for _, p := range ch.projects {
		if project != "" && p.name != project {
			continue
		}
		path := filepath.Join(p.dir, filepath.FromSlash(file))
		if !filepath.IsLocal(filepath.FromSlash(file)) {
			continue
		}
		if src, err := os.ReadFile(path); err == nil {
			wf := &walkFile{project: p.name, dir: p.dir, rel: file, path: path, src: src}
			ch.files = append(ch.files, wf)
			return wf
		}
	}
	return nil
}

func clampLine(l, n int) int { return min(max(l, 1), max(n, 1)) }

// fileLines are src's lines, without their endings.
func fileLines(src []byte) []string {
	s := strings.ReplaceAll(string(src), "\r\n", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

// linesHash is the text of lines [a, b] (1-based) of src: what a step is discarded for when it changes.
func linesHash(src []byte, a, b int) string {
	ls := fileLines(src)
	if a < 1 || b > len(ls) {
		return "gone"
	}
	h := sha256.Sum256([]byte(strings.Join(ls[a-1:b], "\n")))
	return hex.EncodeToString(h[:12])
}
