package codex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
)

// terminalsTimeout bounds asking codex what a turn left running.
const terminalsTimeout = 5 * time.Second

var _ agent.ProcessStopper = (*session)(nil)

// terminal is app-server v2's ThreadBackgroundTerminal.
type terminal struct {
	ItemID     string   `json:"itemId"`
	ProcessID  string   `json:"processId"`
	Command    string   `json:"command"`
	Cwd        string   `json:"cwd"`
	OSPid      *int     `json:"osPid"`
	CPUPercent *float64 `json:"cpuPercent"`
	RSSKb      *int64   `json:"rssKb"`
}

// terminals are the thread's background terminals, which don't wake the agent: none when codex can't say (one too
// old for thread/backgroundTerminals/list, or slow).
func (s *session) terminals() []core.BackgroundTask {
	ctx, cancel := context.WithTimeout(context.Background(), terminalsTimeout)
	defer cancel()
	var out []core.BackgroundTask
	cursor := ""
	for range 100 {
		params := map[string]any{"threadId": s.ID()}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var r struct {
			Data       []terminal `json:"data"`
			NextCursor *string    `json:"nextCursor"`
		}
		if err := s.rpc.Call(ctx, "thread/backgroundTerminals/list", params, &r); err != nil {
			return out
		}
		for _, t := range r.Data {
			b := core.BackgroundTask{ID: t.ProcessID, Kind: "terminal", Description: t.Command}
			if t.OSPid != nil {
				b.PID = *t.OSPid
			}
			if t.CPUPercent != nil {
				b.CPU = *t.CPUPercent
			}
			if t.RSSKb != nil {
				b.Memory = *t.RSSKb << 10
			}
			out = append(out, b)
		}
		if r.NextCursor == nil || *r.NextCursor == "" || *r.NextCursor == cursor {
			return out
		}
		cursor = *r.NextCursor
	}
	return out
}

// StopProcess terminates one of the thread's background terminals.
func (s *session) StopProcess(ctx context.Context, id string) error {
	var r struct {
		Terminated bool `json:"terminated"`
	}
	err := s.rpc.Call(ctx, "thread/backgroundTerminals/terminate", map[string]any{"threadId": s.ID(), "processId": id}, &r)
	var je *jsonrpc.Error
	if errors.As(err, &je) && je.Code == jsonrpc.MethodNotFound {
		return errors.New("this codex can't stop its background terminals: update codex")
	}
	if err != nil {
		return fmt.Errorf("codex thread/backgroundTerminals/terminate: %w", err)
	}
	if !r.Terminated {
		return fmt.Errorf("codex has no process %s running", id)
	}
	return nil
}
