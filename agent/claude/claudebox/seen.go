package claudebox

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"slices"
	"sync"
	"time"
)

// alwaysSeen are .claude.json flags of what Claude Code shows once and waits on (onboarding, notices, nudges,
// terms), set in every sandbox whether or not the host's user has seen them: a sandboxed agent has nobody to
// dismiss one. A newer Claude Code's own are found in its binary (seenIn).
var alwaysSeen = []string{
	"hasCompletedOnboarding", "hasCompletedClaudeInChromeOnboarding", "hasSeenAutoDefaultNudge",
	"hasSeenAutoDefaultNotice", "hasSeenTasksHint", "hasSeenEffortMediumNudge", "hasSeenUltrareviewTerms",
	"hasSeenUltraplanTerms", "hasSeenAutoModeEntryWarning", "hasSeenAutoModeOutsideReadPrompt",
	"hasSeenAutofixPrChatOpsNotice", "hasAcknowledgedCostThreshold",
}

// seenCache is what seenIn found in each binary, by path, size and time.
type seenCache struct {
	mu    sync.Mutex
	found map[seenKey]Seen
}

type seenKey struct {
	path string
	size int64
	mod  time.Time
}

// Seen is what a sandbox's Claude Code is told it has shown already.
type Seen struct {
	Flags   []string // set true
	Version string   // its release notes are read; "" when it isn't known
}

// SeenFor is what the Claude Code at bin has shown once, as far as a sandbox goes: alwaysSeen, the flags its own
// bundle sets, and its version's release notes when it runs here (runs). Each binary is read once.
func (x *Box) SeenFor(bin string, runs bool) Seen {
	st, err := os.Stat(bin)
	if err != nil {
		return Seen{Flags: alwaysSeen}
	}
	k := seenKey{bin, st.Size(), st.ModTime()}
	c := &x.seen
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.found[k]; ok {
		return s
	}
	s := Seen{Flags: seenIn(bin)}
	if runs {
		s.Version, _ = claudeVersion(bin)
	}
	if c.found == nil {
		c.found = map[seenKey]Seen{}
	}
	c.found[k] = s
	return s
}

// seenIn is alwaysSeen and the seen flags the file at path sets.
func seenIn(path string) []string {
	out := slices.Clone(alwaysSeen)
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	const chunk, overlap = 4 << 20, 128 // a flag cut by a chunk's end is whole in the overlap
	r := bufio.NewReaderSize(f, chunk)
	buf := make([]byte, 0, chunk+overlap)
	for {
		n, err := io.ReadFull(r, buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		out = append(out, setFlags(buf)...)
		if err != nil {
			break
		}
		keep := buf[len(buf)-overlap:]
		buf = append(buf[:0], keep...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// setFlags are the flags b's bundle sets true once something's been seen, done or acknowledged:
// hasSeen…:!0, hasCompleted…:!0, hasAcknowledged…:!0.
func setFlags(b []byte) []string {
	var out []string
	for i := 0; ; {
		j := bytes.Index(b[i:], []byte("has"))
		if j < 0 {
			return out
		}
		at := i + j
		i = at + 3
		if at > 0 && ident(b[at-1]) {
			continue
		}
		end := i
		for end < len(b) && ident(b[end]) {
			end++
		}
		name := b[at:end]
		if !bytes.HasPrefix(name, []byte("hasSeen")) && !bytes.HasPrefix(name, []byte("hasCompleted")) &&
			!bytes.HasPrefix(name, []byte("hasAcknowledged")) {
			continue
		}
		if bytes.HasPrefix(b[end:], []byte(":!0")) {
			out = append(out, string(name))
		}
	}
}

func ident(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$'
}
