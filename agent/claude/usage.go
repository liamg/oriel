package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
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

var usage struct {
	mu      sync.Mutex
	last    core.ProviderUsage // the newest reading, a session's or a probe's
	probing bool
}

// remember keeps a reading of the plan's windows for Usage, unless a newer one is kept.
func remember(u core.ProviderUsage) {
	usage.mu.Lock()
	defer usage.mu.Unlock()
	if !u.Sampled.Before(usage.last.Sampled) {
		usage.last = u
	}
}

// readUsage probes /usage when Claude's files record a subscription login and no probe is running.
func (p Provider) readUsage(ctx context.Context) error {
	if _, _, _, ok := subscription(nil); !ok {
		return nil
	}
	usage.mu.Lock()
	if usage.probing {
		usage.mu.Unlock()
		return nil
	}
	usage.probing = true
	usage.mu.Unlock()
	defer func() {
		usage.mu.Lock()
		usage.probing = false
		usage.mu.Unlock()
	}()
	_, err := p.probe(ctx)
	return err
}

func (p Provider) probeDir() string {
	if p.ProbeDir != "" {
		return p.ProbeDir
	}
	return filepath.Join(os.TempDir(), "oriel-claude-usage")
}

// probe runs the hidden session until its mod reports the plan's windows.
func (p Provider) probe(ctx context.Context) (core.ProviderUsage, error) {
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
	sess, err := p.start(ctx, agent.StartSpec{Cwd: dir, StateDir: state}, "")
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
				_, _ = s.term.Write([]byte("/usage\r"))
				typed = true
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
			if typed {
				s.queueCmd(waitOut{Kind: "usage"})
			}
		case <-ctx.Done():
			return core.ProviderUsage{}, fmt.Errorf("claude: no plan windows from /usage: %w", ctx.Err())
		}
	}
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
