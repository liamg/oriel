package worker

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
)

func newID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// mutate changes a session's thread under the worker's edit lock, so two changes to one thread can't race.
func (w *Worker) mutateThread(id core.SessionID, tid core.ThreadID, fn func(t *core.Thread) error) (core.Thread, error) {
	w.edit.Lock()
	defer w.edit.Unlock()
	se, err := w.session(id)
	if err != nil {
		return core.Thread{}, err
	}
	t, ok := se.Thread(tid)
	if !ok {
		return core.Thread{}, fmt.Errorf("no thread %s in %s", tid, id)
	}
	t.Comments = slices.Clone(t.Comments)
	t.Touched = slices.Clone(t.Touched)
	if err := fn(&t); err != nil {
		return core.Thread{}, err
	}
	t.Updated = w.opts.Now().UTC()
	if err := w.emit(core.ThreadUpdated{Session: id, Thread: t}); err != nil {
		return core.Thread{}, err
	}
	return t, nil
}

// findThread resolves a thread id or a unique prefix of one, as the agent may give either.
func findThread(se core.SessionState, ref string) (core.Thread, error) {
	ref = strings.TrimSpace(ref)
	var found []core.Thread
	for _, t := range se.Threads {
		if string(t.ID) == ref {
			return t, nil
		}
		if ref != "" && strings.HasPrefix(string(t.ID), ref) {
			found = append(found, t)
		}
	}
	switch len(found) {
	case 0:
		return core.Thread{}, fmt.Errorf("no thread %q", ref)
	case 1:
		return found[0], nil
	}
	return core.Thread{}, fmt.Errorf("thread %q is ambiguous", ref)
}

// Comment adds your comment to a thread, opening one if needed, and hands it to the agent (agents.md →
// Comments to agent): at once if it's idle, after an interrupt if urgent, else when the turn ends.
func (w *Worker) Comment(ctx context.Context, r CommentReq) (core.ThreadID, error) {
	if strings.TrimSpace(r.Body) == "" && len(r.Images) == 0 {
		return "", errors.New("the comment is empty")
	}
	se, err := w.session(r.Session)
	if err != nil {
		return "", err
	}
	now := w.opts.Now().UTC()
	c := core.Comment{ID: newID(6), Author: core.AuthorYou, Name: whoAmI(ctx), Body: r.Body, Time: now, Images: r.Images, Btw: r.Btw}
	var t core.Thread
	if r.Thread == "" {
		t = core.Thread{ID: core.ThreadID(newID(8)), Anchor: r.Anchor, Title: r.Title, Status: cmp.Or(r.Status, core.ThreadOpen), Urgent: r.Urgent,
			Btw: r.Btw, Comments: []core.Comment{c}, Created: now, Updated: now}
		if err := w.emit(core.ThreadUpdated{Session: r.Session, Thread: t}); err != nil {
			return "", err
		}
	} else {
		if _, err := findThread(se, string(r.Thread)); err != nil {
			return "", err
		}
		t, err = w.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
			t.Comments = append(t.Comments, c)
			if t.Status != core.ThreadOpen && !r.Btw {
				t.Status = core.ThreadOpen
			}
			t.Urgent = t.Urgent || r.Urgent
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	w.restatus(r.Session)
	if r.Btw {
		go w.answerBtw(r.Session, t.ID)
		return t.ID, nil
	}
	if se.Agent.Provider == "" || se.Archived || !t.Open() {
		return t.ID, nil
	}
	h, err := w.hostFor(r.Session, true)
	if err != nil {
		return t.ID, err
	}
	if r.Urgent {
		h.urgent()
	}
	h.kick()
	return t.ID, nil
}

// SetThread changes a thread's status. Resolving one completes the tasks it asked for.
func (w *Worker) SetThread(r ThreadReq) error {
	t, err := w.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
		if r.Status != "" {
			t.Status = r.Status
			if r.Status != core.ThreadOpen {
				t.Busy = false
			}
		}
		if r.Urgent != nil {
			t.Urgent = *r.Urgent
		}
		return nil
	})
	if err != nil {
		return err
	}
	if t.Status == core.ThreadResolved {
		w.completeTasks(r.Session, t.ID)
	}
	if r.Urgent != nil && *r.Urgent && t.WaitsOnAgent() {
		if h, _ := w.hostFor(r.Session, false); h != nil {
			h.urgent()
			h.kick()
		}
	}
	w.restatus(r.Session)
	return nil
}

func (w *Worker) completeTasks(id core.SessionID, tid core.ThreadID) {
	se, err := w.session(id)
	if err != nil {
		return
	}
	for _, task := range se.Tasks {
		if task.Thread == tid && task.Status != core.TaskCompleted {
			task.Status = core.TaskCompleted
			_ = w.emit(core.TaskChanged{Session: id, Task: task})
		}
	}
}

// answerBtw answers a side question from a fork of the agent, outside the conversation.
func (w *Worker) answerBtw(id core.SessionID, tid core.ThreadID) {
	se, err := w.session(id)
	if err != nil {
		return
	}
	t, _ := se.Thread(tid)
	body := ""
	h, _ := w.hostFor(id, false)
	if h == nil || !h.running() {
		body = "No answer: the agent isn't running, so there's nothing to fork."
	} else {
		ans, err := h.fork(btwPrompt(t))
		if err != nil {
			body = "No answer: " + err.Error()
		} else {
			body = ans
		}
	}
	_, _ = w.mutateThread(id, tid, func(t *core.Thread) error {
		t.Comments = append(t.Comments, core.Comment{ID: newID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: body,
			Time: w.opts.Now().UTC(), Btw: true, Model: modelLabel(se.Agent)})
		return nil
	})
	w.restatus(id)
}

func btwPrompt(t core.Thread) string {
	place := ""
	if a := t.Anchor; a != nil {
		place = fmt.Sprintf(" on %s:%d", a.File, a.Line)
		if s := strings.TrimSpace(a.Text); s != "" {
			place += fmt.Sprintf(" (`%s`)", s)
		}
	}
	var said, asked []string
	for i, c := range t.Comments {
		if c.Author == core.AuthorYou && c.Btw && i == len(t.Comments)-1 {
			asked = append(asked, c.Body)
			continue
		}
		said = append(said, string(c.Author)+": "+c.Body)
	}
	parts := []string{"The user asks a side question in Oriel" + place + ". Answer it briefly from what you already know: you have no tools here, and this exchange is not part of the main conversation."}
	if len(said) > 0 {
		parts = append(parts, "The thread so far:")
		parts = append(parts, said...)
	}
	parts = append(parts, "The question:")
	parts = append(parts, asked...)
	return strings.Join(parts, "\n\n")
}

func modelLabel(a core.AgentState) string {
	return strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{a.Model, a.Effort}, func(s string) bool { return s == "" }), " · "))
}

// SetTask adds or changes a task from the app.
func (w *Worker) SetTask(id core.SessionID, t core.Task) error {
	if t.ID == "" {
		t.ID = w.nextTaskID(id)
		t.Created = w.opts.Now().UTC()
	}
	if t.Status == "" {
		t.Status = core.TaskPending
	}
	return w.emit(core.TaskChanged{Session: id, Task: t})
}

func (w *Worker) nextTaskID(id core.SessionID) string {
	se, _ := w.session(id)
	n := 1
	for _, t := range se.Tasks {
		var k int
		if _, err := fmt.Sscanf(t.ID, "%d", &k); err == nil && k >= n {
			n = k + 1
		}
	}
	return fmt.Sprint(n)
}
