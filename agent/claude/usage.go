package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// The /usage probe: a hidden claude session started the way any other is, with Oriel's mod, in Oriel's own folder.
// Once the mod says it's up, /usage is typed, which Claude Code runs itself, so nothing reaches the model; the mod
// then reports $.session.usage() as structured data, asked for until it has the windows.

const (
	probeTimeout = 40 * time.Second
	probeAsk     = 500 * time.Millisecond // how often the mod is asked for the windows
)

// ProbeError is why the /usage probe got no plan windows.
type ProbeError struct {
	Hello  bool   // claude said hello and /usage was typed; false: claude never got that far
	Screen string // the end of what claude last showed
	Usage  string // why $.session.usage() last failed, when it did
	Log    string // claude's debug log of the probe
	Err    error
}

func (e *ProbeError) Error() string {
	msg := fmt.Sprintf("claude: /usage typed but no plan windows came: %v", e.Err)
	if !e.Hello {
		msg = fmt.Sprintf("claude: the /usage session never started: %v", e.Err)
	}
	if e.Usage != "" {
		msg += "; $.session.usage() failed: " + e.Usage
	}
	if e.Screen != "" {
		msg += "; the screen ends: " + e.Screen
	}
	if e.Log != "" {
		msg += "; claude's debug log: " + e.Log
	}
	return msg
}

func (e *ProbeError) Unwrap() error { return e.Err }

func (e *ProbeError) Provider() string { return "claude" }

func (e *ProbeError) Started() bool { return e.Hello }

var _ agent.UsageProbeFailed = (*ProbeError)(nil)

// usageBook is a Provider's readings of the plan's windows.
type usageBook struct {
	mu      sync.Mutex
	last    core.ProviderUsage // the newest reading, a session's or a probe's
	probing *probeRun          // the probe running, nil when none is
	begun   chan struct{}      // closed when the next probe starts, if anyone is waiting for one
}

// started is closed once a probe is running.
func (b *usageBook) started() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.probing != nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	if b.begun == nil {
		b.begun = make(chan struct{})
	}
	return b.begun
}

// probeRun is a probe that callers arriving while it runs wait on: done closes once err is set.
type probeRun struct {
	done chan struct{}
	err  error
}

// remember keeps a reading of the plan's windows for Usage, unless a newer one is kept.
func (b *usageBook) remember(u core.ProviderUsage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !u.Sampled.Before(b.last.Sampled) {
		b.last = u
	}
}

// readUsage probes /usage when claude is installed, or waits for the probe already running. Signed out, there's no
// reading and no error.
func (p *Provider) readUsage(ctx context.Context) error {
	if _, err := exec.LookPath(p.bin()); err != nil {
		return nil
	}
	p.usage.mu.Lock()
	if run := p.usage.probing; run != nil {
		p.usage.mu.Unlock()
		select {
		case <-run.done:
			return run.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	run := &probeRun{done: make(chan struct{})}
	p.usage.probing = run
	if p.usage.begun != nil {
		close(p.usage.begun)
		p.usage.begun = nil
	}
	p.usage.mu.Unlock()
	_, err := p.probe(ctx)
	if errors.As(err, new(agent.AuthExpired)) {
		err = nil
	}
	p.usage.mu.Lock()
	p.usage.probing = nil
	p.usage.mu.Unlock()
	run.err = err
	close(run.done)
	return err
}

func (p *Provider) probeDir() string {
	if p.ProbeDir != "" {
		return p.ProbeDir
	}
	return filepath.Join(os.TempDir(), "oriel-claude-usage")
}

// probe runs the hidden session until its mod reports the plan's windows.
func (p *Provider) probe(ctx context.Context) (core.ProviderUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	dir := p.probeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return core.ProviderUsage{}, err
	}
	state, err := os.MkdirTemp("", "oriel-claude-usage-")
	if err != nil {
		return core.ProviderUsage{}, err
	}
	defer os.RemoveAll(state)
	// Claude Code's own debug log of the last probe, for when it reads no windows.
	logs := filepath.Join(dir, "debug")
	_ = os.RemoveAll(logs)
	if err := os.MkdirAll(logs, 0o755); err != nil { // claude writes to the path itself unless it's a folder
		return core.ProviderUsage{}, err
	}
	sess, err := p.start(ctx, agent.StartSpec{Cwd: dir, StateDir: state, NoMCP: true,
		Env: []string{"DEBUG=1", "CLAUDE_CODE_DEBUG_LOGS_DIR=" + logs}}, "")
	if err != nil {
		return core.ProviderUsage{}, err
	}
	s := sess.(*session)
	defer s.Close()
	ask := time.NewTicker(probeAsk)
	defer ask.Stop()
	typed := false
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				return core.ProviderUsage{}, errors.New("claude: the /usage session ended without the plan's windows")
			}
			switch e := ev.(type) {
			case agent.Ready:
				if !typed {
					_, _ = s.term.Write([]byte("/usage\r"))
					typed = true
				}
			case agent.UsageUpdate:
				s.quit()
				return e.Usage, nil
			case agent.TurnStart:
				_ = s.Interrupt(ctx)
				return core.ProviderUsage{}, errors.New("claude: the /usage session started a turn")
			case agent.AuthExpired:
				return core.ProviderUsage{}, e
			case agent.Exited:
				return core.ProviderUsage{}, fmt.Errorf("claude: the /usage session exited: %s", e.Err)
			}
		case <-ask.C:
			if typed && !s.queued("usage") {
				s.queueCmd(waitOut{Kind: "usage"})
			}
		case <-ctx.Done():
			return core.ProviderUsage{}, &ProbeError{Hello: typed, Screen: s.lastScreen(), Usage: s.usageFailure(),
				Log: latestLog(logs), Err: ctx.Err()}
		}
	}
}

// latestLog is the debug log claude wrote last in dir, which it links as latest; dir when there's no link.
func latestLog(dir string) string {
	if p, err := filepath.EvalSymlinks(filepath.Join(dir, "latest")); err == nil {
		return p
	}
	return dir
}

// quit closes the Usage tab and types /exit, giving claude a moment to go before Close kills it.
func (s *session) quit() {
	_, _ = s.term.Write([]byte("\x1b"))
	time.Sleep(200 * time.Millisecond)
	_, _ = s.term.Write([]byte("/exit\r"))
	select {
	case <-s.done:
	case <-time.After(3 * time.Second):
	}
}
