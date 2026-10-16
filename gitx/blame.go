package gitx

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AuthorKind is who wrote a line, as blame shows it: you, an agent, someone else, or nobody yet.
type AuthorKind int

const (
	ByOther AuthorKind = iota
	ByYou
	ByAgent
	NotCommitted
)

func (k AuthorKind) String() string { return [...]string{"other", "you", "agent", "uncommitted"}[k] }

// BlameLine is the commit that last changed a line. Hash is "" for a line not committed yet.
type BlameLine struct {
	Hash, Author, Email, Summary string
	When                         time.Time
	Kind                         AuthorKind
}

// agentNames are the names that make an author, or a Co-Authored-By trailer, an agent's.
var agentNames = []string{"claude", "codex", "anthropic", "openai", "copilot", "[bot]"}

func isAgent(s string) bool {
	s = strings.ToLower(s)
	for _, n := range agentNames {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// Blame is who last changed each line of contents, path's text in a buffer (saved or not), by git blame --contents.
func (r Repo) Blame(ctx context.Context, path string, contents []byte) ([]BlameLine, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Dir, "--no-pager", "blame", "--porcelain", "--contents", "-", "--", path)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	cmd.Stdin = bytes.NewReader(contents)
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	me := r.userEmail(ctx)
	commits := map[string]*BlameLine{}
	var lines []BlameLine
	var cur *BlameLine
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "\t") {
			lines = append(lines, *cur)
			continue
		}
		key, val, _ := strings.Cut(l, " ")
		if len(key) == 40 && strings.Trim(key, "0123456789abcdef") == "" {
			if c, ok := commits[key]; ok {
				cur = c
			} else {
				cur = &BlameLine{Hash: key}
				commits[key] = cur
			}
			continue
		}
		switch key {
		case "author":
			cur.Author = val
		case "author-mail":
			cur.Email = strings.Trim(val, "<>")
		case "author-time":
			n, _ := strconv.ParseInt(val, 10, 64)
			cur.When = time.Unix(n, 0)
		case "summary":
			cur.Summary = val
		}
	}
	var hashes []string
	for h := range commits {
		if strings.Trim(h, "0") != "" {
			hashes = append(hashes, h)
		}
	}
	coAgent := r.coAuthoredByAgent(ctx, hashes)
	for i := range lines {
		l := &lines[i]
		switch {
		case strings.Trim(l.Hash, "0") == "":
			*l = BlameLine{Kind: NotCommitted}
		case isAgent(l.Author) || isAgent(l.Email) || coAgent[l.Hash]:
			l.Kind = ByAgent
		case me != "" && strings.EqualFold(l.Email, me):
			l.Kind = ByYou
		}
	}
	return lines, sc.Err()
}

// emailFresh is how long a repo's user.email is kept before it's read again.
const emailFresh = 30 * time.Second

var emails = struct {
	sync.Mutex
	by map[string]email
}{by: map[string]email{}}

type email struct {
	addr string
	read time.Time
}

// userEmail is the repo's user.email, lowercased; "" without one.
func (r Repo) userEmail(ctx context.Context) string {
	emails.Lock()
	e, ok := emails.by[r.Dir]
	emails.Unlock()
	if ok && time.Since(e.read) < emailFresh {
		return e.addr
	}
	e = email{read: time.Now()}
	if b, err := r.git(ctx, "config", "user.email"); err == nil {
		e.addr = strings.ToLower(strings.TrimSpace(string(b)))
	}
	emails.Lock()
	emails.by[r.Dir] = e
	emails.Unlock()
	return e.addr
}

// maxTrailers is how many commits' Co-Authored-By readings are kept.
const maxTrailers = 1 << 16

// trailers holds, by commit hash, whether its message has a Co-Authored-By naming an agent: a commit's message never
// changes, so each is read once.
var trailers = struct {
	sync.Mutex
	agent map[string]bool
}{agent: map[string]bool{}}

// coAuthoredByAgent is which of hashes have a Co-Authored-By trailer naming an agent, reading only those not read before.
func (r Repo) coAuthoredByAgent(ctx context.Context, hashes []string) map[string]bool {
	out := map[string]bool{}
	var unread []string
	trailers.Lock()
	for _, h := range hashes {
		if a, ok := trailers.agent[h]; ok {
			out[h] = a
		} else {
			unread = append(unread, h)
		}
	}
	trailers.Unlock()
	if len(unread) == 0 {
		return out
	}
	msgs, err := r.git(ctx, append([]string{"log", "--no-walk=unsorted", "--format=%H%x00%B%x01"}, unread...)...)
	if err != nil {
		return out
	}
	read := map[string]bool{}
	for _, m := range strings.Split(string(msgs), "\x01") {
		h, body, ok := strings.Cut(strings.TrimSpace(m), "\x00")
		if !ok {
			continue
		}
		read[h] = coAuthoredAgent(body)
	}
	trailers.Lock()
	if len(trailers.agent)+len(read) > maxTrailers {
		clear(trailers.agent)
	}
	for h, a := range read {
		trailers.agent[h], out[h] = a, a
	}
	trailers.Unlock()
	return out
}

// AgentTouched is the files commits from base to HEAD by an agent touched: by their author, or a Co-Authored-By
// trailer naming one. Only these can have lines blame calls an agent's.
func (r Repo) AgentTouched(ctx context.Context, base string) (map[string]bool, error) {
	out, err := r.git(ctx, "-c", "core.quotePath=false", "log", "--format=%x01%an%x00%ae%x00%B%x02", "--name-only", base+"..HEAD")
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	for _, c := range strings.Split(string(out), "\x01") {
		head, names, _ := strings.Cut(c, "\x02")
		f := strings.SplitN(head, "\x00", 3)
		if len(f) < 3 || !isAgent(f[0]) && !isAgent(f[1]) && !coAuthoredAgent(f[2]) {
			continue
		}
		for _, n := range strings.Split(names, "\n") {
			if n != "" {
				touched[n] = true
			}
		}
	}
	return touched, nil
}

// coAuthoredAgent says a commit message has a Co-Authored-By trailer naming an agent.
func coAuthoredAgent(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "co-authored-by") && isAgent(v) {
			return true
		}
	}
	return false
}
