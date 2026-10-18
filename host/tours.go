package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/tour"
)

// TourKindID is a tour kind's guidance as an editable prompt.
func TourKindID(kind string) string { return "tour." + kind }

// The tour kinds' guidance is on the Prompts page, under its own group.
func init() {
	for _, k := range tour.Kinds {
		if g := tour.Guidance(k.ID); g != "" {
			if _, err := prompts.Register(prompts.Def{ID: TourKindID(k.ID), Group: "Tour kinds", Label: k.Title, Default: g,
				Help: "How to shape " + article(k.ID) + " " + k.ID + " tour (" + k.Desc + "): what the tour_kind tool tells a tour's agent."}); err != nil {
				panic(err)
			}
		}
	}
}

// tourTools are crit's tour commands as agent tools (RV-8): the kind and how to shape it, the steps, a recorded run,
// and where the export was published. The export itself is the app's tour_export, which colours the code.
func (w *Host) tourTools(h *session) []agent.Tool {
	return []agent.Tool{
		{Name: "tour_kind", Description: "Which kind of tour this is, and how to shape that kind of tour. With kind, change it first: one of " + tourKindList() + ".",
			Schema: schema(`"kind":` + str), Call: h.toolTourKind},
		{Name: "tour_steps", Description: "The tour's steps, in order: the NN-slug.md files at its root.", ReadOnly: true, Schema: schema(""),
			Call: h.toolTourSteps},
		{Name: "tour_run", Description: "Run a command inside the tour and record what it printed, its exit code and when, as a run block at the end of a step " +
			"(step: its number or file; without one the block is returned, not recorded). command: the program and its arguments. cwd: a directory inside the " +
			"tour, such as harness or repos/<name>. timeout: seconds, 60 by default. Never write a run block by hand.",
			Schema: schema(`"command":{"type":"array","items":{"type":"string"}},"step":`+str+`,"cwd":`+str+`,"timeout":`+num, "command"), Call: h.toolTourRun},
		{Name: "tour_share", Description: "Record where the tour's export was published: the link the reader shows.", Schema: schema(`"url":`+str, "url"),
			Call: h.toolTourShare},
	}
}

func tourKindList() string {
	var ids []string
	for _, k := range tour.Kinds {
		ids = append(ids, k.ID)
	}
	return strings.Join(ids, ", ")
}

// tourSession is the calling session, when it's a tour.
func (h *session) tourSession() (core.SessionState, error) {
	se, err := h.w.session(h.id)
	if err != nil {
		return se, err
	}
	if se.Kind != core.KindTour || se.Dir == "" {
		return se, errors.New("this session isn't a tour")
	}
	return se, nil
}

func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// toolTourKind says (or sets) which kind of tour this is, and prints how to shape it.
func (h *session) toolTourKind(_ context.Context, raw json.RawMessage) (string, error) {
	se, err := h.tourSession()
	if err != nil {
		return "", err
	}
	verb := "is"
	if k := strings.TrimSpace(parse(raw).str("kind")); k != "" {
		if err := SetTourKind(se.Dir, k); err != nil {
			return "", err
		}
		verb = "is now"
	}
	info, _ := TourOf(se.Dir)
	out := fmt.Sprintf("This tour %s %s %s tour. Kinds: %s.", verb, article(info.Kind), info.Kind, tourKindList())
	o, _ := h.w.Prompts()
	if g := strings.TrimSpace(o.Text(TourKindID(info.Kind))); g != "" {
		out += fmt.Sprintf("\n\nHow to shape %s %s tour:\n\n%s", article(info.Kind), info.Kind, g)
	}
	return out, nil
}

func (h *session) toolTourSteps(context.Context, json.RawMessage) (string, error) {
	se, err := h.tourSession()
	if err != nil {
		return "", err
	}
	files, err := tour.StepFiles(se.Dir)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No steps yet: write 01-<slug>.md at the tour's root to start it.", nil
	}
	var b strings.Builder
	for i, f := range files {
		fmt.Fprintf(&b, "%2d  %s\n", i+1, f)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// toolTourRun runs a command in the tour and records it in a step. In a sandbox the agent's own shell is the only
// place a command may run, so it's refused there.
func (h *session) toolTourRun(ctx context.Context, raw json.RawMessage) (string, error) {
	se, err := h.tourSession()
	if err != nil {
		return "", err
	}
	if se.Agent.Sandboxed {
		return "", errors.New("in a sandbox Oriel can't run commands for you: say in the step that the output couldn't be recorded")
	}
	var in struct {
		Command []string `json:"command"`
		Step    string   `json:"step"`
		Cwd     string   `json:"cwd"`
		Timeout int      `json:"timeout"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	res, err := tour.RunCommand(ctx, se.Dir, in.Cwd, in.Command, time.Duration(in.Timeout)*time.Second)
	if err != nil {
		return "", err
	}
	fence := res.Fence()
	if in.Step == "" {
		return "Not recorded (no step given):\n\n" + fence, nil
	}
	if err := tour.Append(se.Dir, in.Step, fence); err != nil {
		return "", err
	}
	what := fmt.Sprintf("exit %d", res.Exit)
	if res.TimedOut {
		what += ", killed at the time limit"
	}
	return fmt.Sprintf("Recorded in step %s (%s, %d bytes of output).", tour.MatchStep(se.Dir, in.Step), what, len(res.Output)), nil
}

func (h *session) toolTourShare(_ context.Context, raw json.RawMessage) (string, error) {
	se, err := h.tourSession()
	if err != nil {
		return "", err
	}
	u := strings.TrimSpace(parse(raw).str("url"))
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return "", errors.New("url must be a web address")
	}
	if err := SetTourShare(se.Dir, u, h.w.opts.Now()); err != nil {
		return "", err
	}
	return "Shared: " + u, nil
}
