package agentrun

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// Comment adds your comment to a thread, opening one if needed, and hands it to the agent: at once if it's idle,
// after an interrupt if urgent, else when the turn ends.
func (x *Runner) Comment(ctx context.Context, r CommentReq) (_ core.ThreadID, err error) {
	if strings.TrimSpace(r.Body) == "" && len(r.Images) == 0 {
		return "", errors.New("the comment is empty")
	}
	se, err := x.st.Session(r.Session)
	if err != nil {
		return "", err
	}
	now := x.env.Now().UTC()
	c := core.Comment{ID: hostcore.NewID(6), Author: core.AuthorYou, Name: hostcore.WhoAmI(ctx), Body: r.Body, Time: now, Images: r.Images, Btw: r.Btw}
	var f core.Finding
	if r.Finding != "" {
		var ok bool
		if f, ok = se.Finding(r.Finding); !ok {
			return "", fmt.Errorf("no finding %s", r.Finding)
		}
	}
	var t core.Thread
	if r.Thread == "" {
		c.Severity = r.Severity
		t = core.Thread{ID: core.ThreadID(hostcore.NewID(8)), Anchor: r.Anchor, Title: r.Title, Status: cmp.Or(r.Status, core.ThreadOpen), Urgent: r.Urgent,
			Btw: r.Btw, Outgoing: r.Outgoing, Comments: []core.Comment{c}, Created: now, Updated: now}
		if r.Finding != "" {
			t.Origin = core.OriginAIReview
		}
		if err := x.st.Emit(core.ThreadUpdated{Session: r.Session, Thread: t}); err != nil {
			return "", err
		}
		if r.Finding != "" {
			f.Thread = t.ID
			if err := x.st.Emit(core.FindingsRecorded{Session: r.Session, Findings: []core.Finding{f}}); err != nil {
				return "", err
			}
		}
	} else {
		if _, err := hostcore.FindThread(se, string(r.Thread)); err != nil {
			return "", err
		}
		t, err = x.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
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
	hostcore.Restatus(x.st, r.Session)
	if r.Btw {
		x.env.Go("a btw", func() { x.answerBtw(r.Session, t.ID) })
		return t.ID, nil
	}
	if se.Agent.Provider == "" || se.Archived || !t.Open() || t.Outgoing {
		return t.ID, nil
	}
	h := x.sessionOf(r.Session)
	if !h.running() {
		// A sandbox can take minutes to ready the first time: the comment waits for the agent, the caller doesn't.
		h.startAside()
		return t.ID, nil
	}
	if r.Urgent {
		h.urgent()
	}
	h.kick()
	return t.ID, nil
}

// SetThread changes a thread's status, urgency or title. Resolving one completes the tasks it asked for.
func (x *Runner) SetThread(r ThreadReq) (err error) {
	t, err := x.mutateThread(r.Session, r.Thread, func(t *core.Thread) error {
		if r.Status != "" {
			t.Status = r.Status
			if r.Status != core.ThreadOpen {
				t.Busy, t.Active = false, false
			}
		}
		if r.Urgent != nil {
			t.Urgent = *r.Urgent
		}
		if r.Title != "" {
			t.Title = r.Title
		}
		return nil
	})
	if err != nil {
		return err
	}
	if t.Status == core.ThreadResolved {
		x.completeTasks(r.Session, t.ID)
	}
	if r.Urgent != nil && *r.Urgent && t.WaitsOnAgent() {
		if h, _ := x.sessionFor(r.Session, false); h != nil {
			h.urgent()
			h.kick()
		}
	}
	hostcore.Restatus(x.st, r.Session)
	return nil
}

func (x *Runner) completeTasks(id core.SessionID, tid core.ThreadID) {
	se, err := x.st.Session(id)
	if err != nil {
		return
	}
	for _, task := range se.Tasks {
		if task.Thread == tid && task.Status != core.TaskCompleted {
			task.Status = core.TaskCompleted
			_ = x.st.Emit(core.TaskChanged{Session: id, Task: task})
		}
	}
}

// answerBtw answers a side question from a fork of the agent, outside the conversation.
func (x *Runner) answerBtw(id core.SessionID, tid core.ThreadID) {
	se, err := x.st.Session(id)
	if err != nil {
		return
	}
	t, _ := se.Thread(tid)
	body := ""
	h, _ := x.sessionFor(id, false)
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
	_, _ = x.mutateThread(id, tid, func(t *core.Thread) error {
		t.Comments = append(t.Comments, core.Comment{ID: hostcore.NewID(6), Author: core.AuthorAgent, Name: se.Agent.Provider, Body: body,
			Time: x.env.Now().UTC(), Btw: true, Model: modelLabel(se.Agent, "")})
		return nil
	})
	hostcore.Restatus(x.st, id)
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

func modelLabel(a core.AgentState, ran string) string {
	model := a.Model
	if name := ModelName(ran); name != "" {
		model = name
	}
	return strings.TrimSpace(strings.Join(slices.DeleteFunc([]string{model, a.Effort}, func(s string) bool { return s == "" }), " · "))
}

var modelID = regexp.MustCompile(`^claude-([a-z]+)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?$`)

// ModelName is a model's id as people say it: claude-opus-5-5 is "Opus 5.5"; "" for an id it can't read.
func ModelName(id string) string {
	m := modelID.FindStringSubmatch(id)
	if m == nil {
		return ""
	}
	name := strings.ToUpper(m[1][:1]) + m[1][1:] + " " + m[2]
	if m[3] != "" {
		name += "." + m[3]
	}
	return name
}

// SetTask adds or changes a task from the app.
func (x *Runner) SetTask(id core.SessionID, t core.Task) (err error) {
	if t.ID == "" {
		t.ID = x.nextTaskID(id)
		t.Created = x.env.Now().UTC()
	}
	if t.Status == "" {
		t.Status = core.TaskPending
	}
	return x.st.Emit(core.TaskChanged{Session: id, Task: t})
}

func (x *Runner) nextTaskID(id core.SessionID) string {
	se, _ := x.st.Session(id)
	n := 1
	for _, t := range se.Tasks {
		var k int
		if _, err := fmt.Sscanf(t.ID, "%d", &k); err == nil && k >= n {
			n = k + 1
		}
	}
	return fmt.Sprint(n)
}
