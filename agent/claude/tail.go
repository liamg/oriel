package claude

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/fswatch"
)

const (
	// tailPoll is how often the logs are read for what's new when they can't be watched.
	tailPoll = 200 * time.Millisecond
	// tailSafety is how often they're read anyway while watched.
	tailSafety = 5 * time.Second
)

// tailer follows a session's log and its subagents', and turns what they add into events.
type tailer struct {
	mu     sync.Mutex
	config string
	prices price.Books
	cwd    string
	id     string
	main   *transcript.Reader
	subs   map[string]*transcript.Reader
	skip   map[string]int64 // where to start the logs already on disk: a resumed session's history isn't news
	said   string           // the main loop's latest prose this turn
	// answered is told of each answer the API gave: a request that worked.
	answered func(emit func(agent.Event))
	moved    chan struct{} // the session's id or folder changed: watch its new log
	reads    int           // flushes, for the idle tests
	watcher  func() (*fswatch.Watcher, error)
}

func newTailer(config, cwd, id string, resume bool, prices price.Books) *tailer {
	t := &tailer{config: config, prices: prices, cwd: cwd, id: id, subs: map[string]*transcript.Reader{}, skip: map[string]int64{},
		moved: make(chan struct{}, 1), watcher: fswatch.New}
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
		t.move()
	}
}

func (t *tailer) setCwd(cwd string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cwd != t.cwd {
		t.cwd = cwd
		t.move()
	}
}

func (t *tailer) move() {
	select {
	case t.moved <- struct{}{}:
	default:
	}
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

// follow reads the logs until done closes: when they change, and now and then anyway; it polls when they can't be
// watched.
func (t *tailer) follow(done <-chan struct{}, emit func(agent.Event)) {
	if !t.watch(done, emit) {
		t.poll(done, emit)
	}
}

// watch reads the logs as they change until done closes, and is false when they can't be watched.
func (t *tailer) watch(done <-chan struct{}, emit func(agent.Event)) bool {
	w, err := t.watcher()
	if err != nil {
		return false
	}
	defer w.Close()
	var watched []string
	retarget := func() bool {
		want := t.logs()
		if slices.Equal(want, watched) {
			return true
		}
		for _, p := range watched {
			_ = w.Remove(p)
		}
		watched = nil
		for _, p := range want {
			if err := w.Add(p); err != nil {
				return false
			}
			watched = append(watched, p)
		}
		return true
	}
	if !retarget() {
		return false
	}
	t.flush(emit)
	tick := time.NewTicker(tailSafety)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return true
		case ev, ok := <-w.Events():
			if !ok || !drain(w, ev) {
				return false
			}
		case <-t.moved:
		case <-tick.C:
		}
		t.flush(emit)
		if !retarget() {
			return false
		}
	}
}

// drain takes the rest of a burst's events, and is false when the watch broke.
func drain(w *fswatch.Watcher, ev fswatch.Event) bool {
	for {
		if ev.Err != nil {
			return false
		}
		select {
		case next, ok := <-w.Events():
			if !ok {
				return false
			}
			ev = next
		default:
			return true
		}
	}
}

// poll reads the logs every tailPoll until done closes.
func (t *tailer) poll(done <-chan struct{}, emit func(agent.Event)) {
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

// logs are what to watch: the session's log and its subagents' folder.
func (t *tailer) logs() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	p := transcript.Path(t.config, t.cwd, t.id)
	if t.main != nil {
		p = t.main.Path
	}
	return []string{p, filepath.Join(strings.TrimSuffix(p, ".jsonl"), "subagents")}
}

// flush reads everything the logs have now.
func (t *tailer) flush(emit func(agent.Event)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads++
	if t.main == nil {
		p := transcript.Path(t.config, t.cwd, t.id)
		if _, err := os.Stat(p); err != nil {
			return
		}
		t.main = &transcript.Reader{Path: p, Offset: t.skip[p], Prices: t.prices}
	}
	t.emit(t.main.Read(), emit)
	for _, p := range transcript.Subagents(t.main.Path) {
		r := t.subs[p]
		if r == nil {
			r = &transcript.Reader{Path: p, Offset: t.skip[p], Prices: t.prices}
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
