package agentbus

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Log events.
const (
	EventReceived     = "received"
	EventAnswered     = "answered"
	EventLater        = "later"
	EventIgnored      = "ignored"
	EventClaimed      = "claimed"
	EventReviewed     = "reviewed"
	EventCancelled    = "cancelled"
	EventAutoApproved = "auto-approved"
	// A review or a question this person sent, and what came of the review.
	EventRequested       = "requested"
	EventAsked           = "asked"
	EventRequestClaimed  = "request-claimed"
	EventRequestReviewed = "request-reviewed"
)

// LogEntry is one thing that happened to a question or review request.
type LogEntry struct {
	At    time.Time `json:"at"`
	ID    string    `json:"id"`
	Kind  string    `json:"kind"`
	Event string    `json:"event"`
	From  From      `json:"from"`
	// Text is the question, the PR, the answer or the review's summary.
	Text string `json:"text,omitempty"`
	// Detail is the review's note, who answered (human or agent), or the review's state.
	Detail string `json:"detail,omitempty"`
}

// maxLog is how many entries the log keeps.
const maxLog = 2000

func (b *Bus) log(it Item, event, text, detail string) {
	e := LogEntry{At: time.Now().UTC(), ID: it.ID, Kind: it.Kind, Event: event, From: it.From, Text: text, Detail: detail}
	defer b.changed()
	b.logMu.Lock()
	defer b.logMu.Unlock()
	if b.cfg.LogPath == "" {
		b.memLog = append(b.memLog, e)
		if len(b.memLog) > maxLog {
			b.memLog = b.memLog[len(b.memLog)-maxLog:]
		}
		return
	}
	line, _ := json.Marshal(e)
	if err := os.MkdirAll(filepath.Dir(b.cfg.LogPath), 0o700); err != nil {
		log.Printf("agentbus: log: %v", err)
		return
	}
	f, err := os.OpenFile(b.cfg.LogPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("agentbus: log: %v", err)
		return
	}
	_, err = f.Write(append(line, '\n'))
	f.Close()
	if err != nil {
		log.Printf("agentbus: log: %v", err)
		return
	}
	b.logN++
	if b.logN%100 == 0 {
		b.trimLog()
	}
}

// trimLog keeps the newest maxLog entries. Called with logMu held.
func (b *Bus) trimLog() {
	all := readLog(b.cfg.LogPath)
	if len(all) <= maxLog {
		return
	}
	all = all[len(all)-maxLog:]
	tmp := b.cfg.LogPath + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	enc := json.NewEncoder(f)
	for _, e := range all {
		enc.Encode(e)
	}
	if f.Close() == nil {
		os.Rename(tmp, b.cfg.LogPath)
	}
}

func readLog(path string) []LogEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []LogEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var e LogEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Log is up to limit entries, newest first.
func (b *Bus) Log(limit int) []LogEntry {
	b.logMu.Lock()
	all := b.memLog
	if b.cfg.LogPath != "" {
		all = readLog(b.cfg.LogPath)
	}
	b.logMu.Unlock()
	out := make([]LogEntry, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out
}
