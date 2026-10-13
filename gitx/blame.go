package gitx

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	me := ""
	if b, err := r.git(ctx, "config", "user.email"); err == nil {
		me = strings.ToLower(strings.TrimSpace(string(b)))
	}
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
	// Trailers aren't in blame's output: read the commits' messages once.
	var hashes []string
	for h := range commits {
		if strings.Trim(h, "0") != "" {
			hashes = append(hashes, h)
		}
	}
	coAgent := map[string]bool{}
	if len(hashes) > 0 {
		if msgs, err := r.git(ctx, append([]string{"log", "--no-walk=unsorted", "--format=%H%x00%B%x01"}, hashes...)...); err == nil {
			for _, m := range strings.Split(string(msgs), "\x01") {
				h, body, ok := strings.Cut(strings.TrimSpace(m), "\x00")
				if !ok {
					continue
				}
				for _, line := range strings.Split(body, "\n") {
					if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "co-authored-by") && isAgent(v) {
						coAgent[h] = true
					}
				}
			}
		}
	}
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
