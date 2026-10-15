package tour

import (
	"cmp"
	"context"
	"fmt"
	"image"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	// Registered so a screenshot's size can be read: annotations are placed against its real shape.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/liamg/oriel/gitx"
)

// A quote longer than collapseAbove lines hides the stretches nobody marked, contextLines either side of a mark.
const (
	collapseAbove = 60
	contextLines  = 4
)

// Repo is a repository the tour reads from: a project of its session, pinned at Commit.
type Repo struct {
	Name   string `json:"name"`
	Repo   string `json:"repo,omitempty"` // the checkout it's a worktree of
	Dir    string `json:"dir"`            // the worktree, absolute
	Base   string `json:"base,omitempty"`
	Commit string `json:"commit,omitempty"`
	Gone   bool   `json:"gone,omitempty"` // the worktree isn't there any more
}

// Info is what a tour says about itself, beside its steps.
type Info struct {
	Name     string    `json:"name"`
	Title    string    `json:"title"` // the question it answers
	Kind     string    `json:"kind"`
	ShareURL string    `json:"shareUrl,omitempty"` // where its export was published
	SharedAt time.Time `json:"sharedAt,omitzero"`
	Repos    []Repo    `json:"repos"`
}

// Tour is a tour read: its steps, with every quote's code read from its worktree.
type Tour struct {
	Info
	Root  string
	Steps []Step
}

// Step is one page of a tour.
type Step struct {
	File  string // 04-session-tokens.md
	Title string // its first heading, else its file's slug
	Items []Item
}

// Item is one thing on a step, in order.
type Item struct {
	Kind    BlockKind
	Text    string // prose, a note with no quote above it, an example's code, a diagram's source, a run's output
	Lang    string
	Caption string
	Quote   *QuoteText
	Run     *RunInfo
	Shot    *ShotInfo
}

// QuoteText is a quote read from its worktree (or Rev), with its notes pinned under their lines.
type QuoteText struct {
	Repo    string // the project's name
	Dir     string // its worktree
	Path    string // relative to Dir
	Rev     string // another revision it was read at; "" for the pinned worktree
	Start   int
	End     int
	Lines   []string // Start through End
	Src     []byte   // the whole file, for highlighting
	Marked  map[int]bool
	Notes   []PinnedNote // pinned to a line in the quote
	Loose   []PinnedNote // naming a line that isn't in it
	Clipped bool         // the range ran past the end of the file
	Err     string       // why it couldn't be read; the rest is empty

	collapsed bool
	near      map[int]bool
}

// PinnedNote is a note pinned under a line (or a range starting at it).
type PinnedNote struct {
	Line, End int
	Text      string
}

// RunInfo is a recorded command.
type RunInfo struct {
	Cmd, Cwd string
	Exit     int
	At       time.Time
}

// ShotInfo is a screenshot and its annotations.
type ShotInfo struct {
	Src  string // as the step names it, relative to the tour
	Path string // the file; "" when it isn't in the tour
	Alt  string
	W, H int // its size in pixels; 1600×1000 when it can't be read
	Mark []Mark
}

// NotesAt are the notes pinned under line n.
func (q *QuoteText) NotesAt(n int) []PinnedNote {
	var out []PinnedNote
	for _, p := range q.Notes {
		if p.Line == n {
			out = append(out, p)
		}
	}
	return out
}

// Collapsed reports whether the quote hides lines nobody marked.
func (q *QuoteText) Collapsed() bool { return q.collapsed }

// Visible reports whether line n shows while the quote is collapsed: near a mark, or with a note under it.
func (q *QuoteText) Visible(n int) bool {
	return !q.collapsed || q.near[n] || len(q.NotesAt(n)) > 0
}

// Hidden is how many lines a collapsed quote hides.
func (q *QuoteText) Hidden() int {
	if !q.collapsed {
		return 0
	}
	h := 0
	for n := q.Start; n <= q.End; n++ {
		if !q.Visible(n) {
			h++
		}
	}
	return h
}

// Quotes counts the tour's quotes.
func (t Tour) Quotes() int {
	n := 0
	for _, s := range t.Steps {
		for _, it := range s.Items {
			if it.Kind == Quote {
				n++
			}
		}
	}
	return n
}

// Loader reads tours, keeping each file it quotes until it changes.
type Loader struct {
	mu    sync.Mutex
	cache map[string][]byte
}

func NewLoader() *Loader { return &Loader{cache: map[string][]byte{}} }

// Load reads every step of the tour at root.
func (l *Loader) Load(ctx context.Context, root string, info Info) (Tour, error) {
	t := Tour{Info: info, Root: root}
	for i, r := range t.Repos {
		if fi, err := os.Stat(r.Dir); err != nil || !fi.IsDir() {
			t.Repos[i].Gone = true
		}
	}
	files, err := StepFiles(root)
	if err != nil {
		return t, err
	}
	for _, f := range files {
		src, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		t.Steps = append(t.Steps, l.step(ctx, root, t.Repos, f, string(src)))
	}
	return t, nil
}

// step reads one step: its title is its first heading, which is lifted out, and each note is pinned under its line
// of the quote above it.
func (l *Loader) step(ctx context.Context, root string, repos []Repo, file, src string) Step {
	s := Step{File: file}
	var quote *QuoteText
	for _, b := range Parse(src) {
		if b.Kind != Note {
			quote = nil
		}
		switch b.Kind {
		case Prose:
			text := b.Body
			if s.Title == "" {
				if h, rest, ok := firstHeading(text); ok {
					s.Title, text = h, rest
				}
			}
			if strings.TrimSpace(text) != "" {
				s.Items = append(s.Items, Item{Kind: Prose, Text: strings.Trim(text, "\n")})
			}
		case Quote:
			quote = l.quote(ctx, repos, b)
			s.Items = append(s.Items, Item{Kind: Quote, Lang: quote.lang(b.Lang), Quote: quote})
		case Note:
			start, end := LineRange(b.Arg)
			if quote == nil || quote.Err != "" {
				s.Items = append(s.Items, Item{Kind: Note, Text: b.Body})
				continue
			}
			quote.pin(PinnedNote{Line: start, End: end, Text: b.Body})
		case Example:
			s.Items = append(s.Items, Item{Kind: Example, Lang: b.Lang, Text: b.Body})
		case Diagram:
			s.Items = append(s.Items, Item{Kind: Diagram, Lang: b.Lang, Text: strings.TrimSpace(b.Body), Caption: b.Attrs["caption"]})
		case Run:
			r := &RunInfo{Cmd: cmp.Or(b.Attrs["cmd"], strings.TrimSpace(b.Arg)), Cwd: b.Attrs["cwd"], Exit: b.intAttr("exit")}
			r.At, _ = time.Parse(time.RFC3339, b.Attrs["at"])
			s.Items = append(s.Items, Item{Kind: Run, Text: strings.TrimRight(b.Body, "\n"), Run: r})
		case Shot:
			s.Items = append(s.Items, Item{Kind: Shot, Caption: b.Attrs["caption"], Shot: shot(root, b)})
		}
	}
	if s.Title == "" {
		s.Title = titleFromFile(file)
	}
	return s
}

var heading = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// firstHeading lifts a leading "# Title" off the top of a step.
func firstHeading(text string) (title, rest string, ok bool) {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		m := heading.FindStringSubmatch(strings.TrimRight(l, " \t"))
		if m == nil || len(m[1]) != 1 {
			return "", text, false
		}
		return strings.TrimSpace(m[2]), strings.Join(lines[i+1:], "\n"), true
	}
	return "", text, false
}

// pin puts a note under the line it names; one naming a line the quote doesn't have goes at its end, never lost.
// A note brings back the line it's under if the quote's collapse hid it.
func (q *QuoteText) pin(n PinnedNote) {
	if n.Line >= q.Start && n.Line <= q.End {
		q.Notes = append(q.Notes, n)
		return
	}
	q.Loose = append(q.Loose, n)
}

func (q *QuoteText) lang(fence string) string {
	if fence != "" {
		return fence
	}
	return strings.TrimPrefix(filepath.Ext(q.Path), ".")
}

// quote reads a quote's lines from its project's worktree, or at Rev; what goes wrong is in Err.
func (l *Loader) quote(ctx context.Context, repos []Repo, b Block) *QuoteText {
	rel := strings.TrimSpace(b.Attrs["path"])
	q := &QuoteText{Repo: b.Attrs["repo"], Path: rel, Rev: b.Attrs["rev"], Marked: MarkSet(b.Attrs["mark"])}
	start, end := LineRange(b.Attrs["lines"])
	q.Start, q.End = start, end
	p, ok := project(repos, q.Repo)
	switch {
	case !ok:
		q.Err = fmt.Sprintf("unknown repo %q: add it to the tour before quoting it", q.Repo)
		return q
	case p.Gone:
		q.Err = fmt.Sprintf("the worktree for %s is gone", p.Name)
		return q
	case !safeRel(rel):
		q.Err = "that path leaves the repository"
		return q
	}
	q.Repo, q.Dir = p.Name, p.Dir
	src, err := l.file(ctx, p, q.Rev, rel)
	if err != nil {
		at := cmp.Or(q.Rev, shortSHA(p.Commit), "the worktree")
		q.Err = fmt.Sprintf("%s is not in %s at %s", rel, p.Name, at)
		return q
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n"), "\n")
	q.Start = max(q.Start, 1)
	q.Clipped = end > len(lines)
	if q.End <= 0 || q.End > len(lines) {
		q.End = len(lines)
	}
	if q.Start > len(lines) {
		q.Err = fmt.Sprintf("%s has only %d lines", rel, len(lines))
		return q
	}
	q.Src, q.Lines = src, lines[q.Start-1:q.End]
	if q.End-q.Start+1 > collapseAbove && len(q.Marked) > 0 {
		q.collapsed, q.near = true, map[int]bool{}
		for n := range q.Marked {
			for i := n - contextLines; i <= n+contextLines; i++ {
				q.near[i] = true
			}
		}
		if q.Hidden() == 0 {
			q.collapsed = false
		}
	}
	return q
}

// file is a project's file in its worktree, or at a revision; a revision is kept as it is, the worktree's while its
// size and time stay the same.
func (l *Loader) file(ctx context.Context, p Repo, rev, rel string) ([]byte, error) {
	key := p.Dir + "\x00" + rev + "\x00" + rel
	if rev == "" {
		fi, err := os.Stat(filepath.Join(p.Dir, rel))
		if err != nil {
			return nil, err
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("%s is a directory", rel)
		}
		key += "\x00" + strconv.FormatInt(fi.ModTime().UnixNano(), 10) + "\x00" + strconv.FormatInt(fi.Size(), 10)
	}
	l.mu.Lock()
	b, ok := l.cache[key]
	l.mu.Unlock()
	if ok {
		return b, nil
	}
	var err error
	if rev == "" {
		b, err = os.ReadFile(filepath.Join(p.Dir, rel))
	} else if b, ok = (gitx.Repo{Dir: p.Dir}).FileAt(ctx, rev, filepath.ToSlash(rel)); !ok {
		err = fmt.Errorf("%s is not at %s", rel, rev)
	}
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	if len(l.cache) > 512 {
		clear(l.cache)
	}
	l.cache[key] = b
	l.mu.Unlock()
	return b, nil
}

// project finds a repo by its name, else by its checkout's directory name, so repo=api works however it was named.
func project(repos []Repo, name string) (Repo, bool) {
	name = strings.TrimSpace(name)
	for _, p := range repos {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	for _, p := range repos {
		if p.Repo != "" && strings.EqualFold(filepath.Base(p.Repo), name) {
			return p, true
		}
	}
	return Repo{}, false
}

func safeRel(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	clean := path.Clean(filepath.ToSlash(rel))
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// shot reads a screenshot fence: its file and size, and its annotations.
func shot(root string, b Block) *ShotInfo {
	s := &ShotInfo{Src: strings.TrimSpace(cmp.Or(b.Attrs["src"], b.Arg)), Alt: cmp.Or(b.Attrs["alt"], b.Attrs["caption"]), W: 1600, H: 1000,
		Mark: ParseMarks(b.Body)}
	p, err := AssetPath(root, s.Src)
	if err != nil {
		return s
	}
	s.Path = p
	if f, err := os.Open(p); err == nil {
		if cfg, _, err := image.DecodeConfig(f); err == nil && cfg.Width > 0 && cfg.Height > 0 {
			s.W, s.H = cfg.Width, cfg.Height
		}
		_ = f.Close()
	}
	return s
}

// LineLabel is "42" or "42-56".
func LineLabel(start, end int) string {
	if end <= start {
		return strconv.Itoa(start)
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// ShortSHA is a commit's first seven characters.
func ShortSHA(s string) string { return shortSHA(s) }

func titleFromFile(f string) string {
	base := strings.TrimSuffix(filepath.Base(f), ".md")
	if i := strings.Index(base, "-"); i > 0 {
		if _, err := strconv.Atoi(base[:i]); err == nil {
			base = base[i+1:]
		}
	}
	base = strings.ReplaceAll(base, "-", " ")
	if base == "" {
		return f
	}
	return strings.ToUpper(base[:1]) + base[1:]
}
