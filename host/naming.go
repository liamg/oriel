package host

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
)

// namerPack is what the naming agent is told.
const namerPack = `You name a coding session from the request it starts with.
Reply with the name only: 2 to 6 words, sentence case, plain words, no quotes, no full stop.
Say what the work is, not how it was asked. Example: "Paste images into chat".`

// nameWait is how long a name may take before the session keeps the one it has.
const nameWait = 2 * time.Minute

// autoName has a small model name a new session and its first request from prompt. A name or title the user has
// changed meanwhile stays.
func (w *Host) autoName(id core.SessionID, was, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), nameWait)
	defer cancel()
	name, err := w.askName(ctx, prompt)
	if err != nil {
		w.log.Warn("naming the session", "session", id, "err", err)
		return
	}
	if name == "" {
		return
	}
	se, err := w.session(id)
	if err != nil {
		return
	}
	if se.Name == was {
		if err := w.Rename(id, name); err != nil {
			w.log.Warn("naming the session", "session", id, "err", err)
		}
	}
	first := core.Summary(prompt)
	for _, t := range se.Threads {
		if t.Title == first {
			if err := w.SetThread(ThreadReq{Session: id, Thread: t.ID, Title: name}); err != nil {
				w.log.Warn("naming the request", "session", id, "err", err)
			}
			break
		}
	}
}

// askName runs the naming agent on prompt and returns its answer, cleaned.
func (w *Host) askName(ctx context.Context, prompt string) (string, error) {
	if len(prompt) > 4000 {
		prompt = prompt[:4000]
	}
	u, err := w.StartUtility(ctx, UtilitySpec{Name: "namer", Pack: namerPack, Prompt: prompt})
	if err != nil {
		return "", err
	}
	defer u.Close()
	var said string
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-w.closed:
			return "", nil
		case ev, ok := <-u.Events():
			if !ok {
				return cleanName(said), nil
			}
			switch ev.Type {
			case "message":
				said = ev.Text
			case "turn-end", "exited":
				return cleanName(said), nil
			}
		}
	}
}

// cleanName is the agent's answer as a name: its first line, unquoted, at most 60 characters.
func cleanName(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimRight(strings.Trim(strings.TrimSpace(s), "\"'`*"), ".")
	if utf8.RuneCountInString(s) > 60 {
		s = strings.TrimSpace(string([]rune(s)[:60]))
	}
	return s
}
