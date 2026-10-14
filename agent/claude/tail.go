package claude

import (
	"os"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
)

// tailPoll is how often the log is read for what's new.
const tailPoll = 200 * time.Millisecond

// tailer follows a session's log and its subagents', and turns what they add into events.
type tailer struct {
	mu     sync.Mutex
	config string
	cwd    string
	id     string
	main   *transcript.Reader
	subs   map[string]*transcript.Reader
	skip   map[string]int64 // where to start the logs already on disk: a resumed session's history isn't news
	said   string           // the main loop's latest prose this turn
	// answered is told of each answer the API gave: a request that worked.
	answered func(emit func(agent.Event))
}

func newTailer(config, cwd, id string, resume bool) *tailer {
	t := &tailer{config: config, cwd: cwd, id: id, subs: map[string]*transcript.Reader{}, skip: map[string]int64{}}
	if resume {
		p := transcript.Path(config, cwd, id)
		for _, f := range append([]string{p}, transcript.Subagents(p)...) {
			if fi, err := os.Stat(f); err == nil {
				t.skip[f] = fi.Size()
			}
		}
	}
	return t
}

func (t *tailer) setID(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if id != t.id {
		t.id, t.main = id, nil
	}
}

func (t *tailer) setCwd(cwd string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cwd = cwd
}

func (t *tailer) startTurn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.said = ""
}

func (t *tailer) usage() transcript.Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.main == nil {
		return transcript.Usage{}
	}
	return t.main.Usage
}

// follow reads the logs until done closes.
func (t *tailer) follow(done <-chan struct{}, emit func(agent.Event)) {
	tick := time.NewTicker(tailPoll)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return
		case <-tick.C:
			t.flush(emit)
		}
	}
}

// flush reads everything the logs have now.
func (t *tailer) flush(emit func(agent.Event)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.main == nil {
		p := transcript.Path(t.config, t.cwd, t.id)
		if _, err := os.Stat(p); err != nil {
			return
		}
		t.main = &transcript.Reader{Path: p, Offset: t.skip[p]}
	}
	t.emit(t.main.Read(), emit)
	for _, p := range transcript.Subagents(t.main.Path) {
		r := t.subs[p]
		if r == nil {
			r = &transcript.Reader{Path: p, Offset: t.skip[p]}
			t.subs[p] = r
		}
		t.emit(r.Read(), emit)
	}
}

func (t *tailer) emit(entries []transcript.Entry, emit func(agent.Event)) {
	for _, e := range entries {
		if e.Kind == transcript.Text && e.Role == "assistant" && e.Agent == "" {
			t.said = e.Text
		}
		if ev := fromEntry(e); ev != nil {
			emit(ev)
		}
		if e.Kind == transcript.Text && e.Role == "assistant" && e.APIError == "" && t.answered != nil {
			t.answered(emit)
		}
	}
}

// flushUntil reads the logs until the turn's answer has been read from them, or for a couple of seconds: the hook
// that ends a turn can run before the log has its last words.
func (t *tailer) flushUntil(answer string, emit func(agent.Event)) {
	answer = strings.TrimSpace(strings.TrimSuffix(answer, "\n…"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		t.flush(emit)
		t.mu.Lock()
		said := t.said
		t.mu.Unlock()
		if answer == "" || (said != "" && (strings.Contains(answer, said) || strings.Contains(said, answer))) || time.Now().After(deadline) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
