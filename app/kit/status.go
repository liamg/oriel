package kit

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// StatusOrder is the statuses, most urgent first.
var StatusOrder = []core.Status{core.StatusNeedsYou, core.StatusPermission, core.StatusWorking, core.StatusCI, core.StatusReviewer,
	core.StatusQuiet, core.StatusOff}

// StatusName is a status as words: "needs you".
func StatusName(s core.Status) string { return strings.ReplaceAll(string(s), "_", " ") }

// Why is what a session's status is about, shortly: what it asks, what it's running, when it last replied.
func Why(s core.SessionState, now time.Time) string {
	switch s.Status {
	case core.StatusPermission, core.StatusNeedsYou:
		if len(s.Asks) > 0 {
			a := s.Asks[0]
			if a.Tool != "" {
				return a.Tool + ": " + FirstLine(a.Summary)
			}
			return FirstLine(cmp.Or(a.Summary, a.Host))
		}
		if s.Agent.Error != "" {
			return FirstLine(s.Agent.Error)
		}
		for _, it := range slices.Backward(s.Transcript) {
			if !it.Time.IsZero() {
				return "replied " + Ago(now, it.Time)
			}
		}
	case core.StatusWorking:
		if st := s.Agent.Starting; st != nil {
			return "starting · " + st.Step
		}
		n := 0
		for _, a := range s.Subagents {
			if a.Running {
				n++
			}
		}
		if n > 0 {
			return Counted(n, "subagent")
		}
		for _, it := range slices.Backward(s.Transcript) {
			if it.Kind == core.ItemTool && !it.Done {
				return ToolLine(it) + " · " + Duration(now.Sub(it.Time))
			}
		}
		if !s.Agent.TurnStarted.IsZero() {
			return "working · " + Duration(now.Sub(s.Agent.TurnStarted))
		}
	case core.StatusCI:
		return "CI running"
	case core.StatusReviewer:
		return "in review"
	}
	return ""
}

// ToolLine is a tool call as one short line: its title, or the command it runs.
func ToolLine(it core.Item) string {
	if it.Title != "" {
		return FirstLine(it.Title)
	}
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(it.Input), &in) == nil && in.Command != "" {
		return FirstLine(in.Command)
	}
	return it.Tool
}
