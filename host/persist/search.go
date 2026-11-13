package persist

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"

	"github.com/liamg/oriel/core"
)

type SearchReq struct {
	Query    string `json:"query"`
	Archived bool   `json:"archived,omitempty"` // archived sessions too
}

// Hit is a match in a session's threads or transcript.
type Hit struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread,omitempty"`
	Item    string         `json:"item,omitempty"`
	Text    string         `json:"text"`
}

// Search finds text in sessions' threads and transcripts: archived ones keep theirs, so they stay searchable.
func (l *Logs) Search(q SearchReq) (out []Hit) {
	defer l.env.Recover("Search", nil)
	needle := strings.ToLower(strings.TrimSpace(q.Query))
	if needle == "" {
		return nil
	}
	for _, se := range l.st.Snapshot().Sessions {
		if se.Archived && !q.Archived {
			continue
		}
		for _, t := range se.Threads {
			for _, c := range t.Comments {
				if strings.Contains(strings.ToLower(c.Body), needle) {
					out = append(out, Hit{Session: se.ID, Thread: t.ID, Text: snippet(c.Body, needle)})
				}
			}
		}
		for _, it := range l.Transcript(se.ID) {
			if strings.Contains(strings.ToLower(it.Text), needle) {
				out = append(out, Hit{Session: se.ID, Item: it.ID, Text: snippet(it.Text, needle)})
			}
		}
	}
	return out
}

// Transcript is a session's whole transcript from its log, which keeps more than its state does.
func (l *Logs) Transcript(id core.SessionID) []core.Item {
	f, err := os.Open(l.eventsPath(id))
	if err != nil {
		return nil
	}
	defer f.Close()
	var items []core.Item
	seen := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !strings.Contains(string(line), `"TranscriptAppended"`) {
			continue
		}
		var rec struct {
			Event core.TranscriptAppended `json:"event"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		for _, it := range rec.Event.Items {
			if i, ok := seen[it.ID]; ok && it.ID != "" {
				items[i] = it
				continue
			}
			seen[it.ID] = len(items)
			items = append(items, it)
		}
	}
	return items
}

func snippet(s, needle string) string {
	i := strings.Index(strings.ToLower(s), needle)
	if i < 0 {
		return s
	}
	from, to := max(0, i-40), min(len(s), i+len(needle)+40)
	return strings.TrimSpace(strings.ReplaceAll(s[from:to], "\n", " "))
}
