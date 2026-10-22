package aireview

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// aiReviewRun is one AI review as it runs.
type aiReviewRun struct {
	x       *Reviews
	session core.SessionID
	review  core.AIReview

	mu    sync.Mutex
	found int
}

func (r *aiReviewRun) tools() []agent.Tool {
	sev, str := `{"type":"string","enum":["critical","high","medium","low","nit"]}`, hostcore.StrType
	return []agent.Tool{
		{Name: "finding", Description: "Report one finding of the review: severity, a one-word category (correctness, tests, api, security, performance, style, docs), a title of a few words, the file (relative to its project) and line in the new code, a body of one to three sentences, project when the session has several, and repo when it's about a repository outside the session.",
			Schema: hostcore.Schema(`"severity":`+sev+`,"category":`+str+`,"title":`+str+`,"body":`+str+`,"file":`+str+`,"line":`+hostcore.IntType+`,"project":`+str+`,"repo":`+str, "severity", "title"),
			Call:   r.toolFinding},
		{Name: "projects", Description: "List the session's projects: name, directory, branch, and whether it has uncommitted changes.", ReadOnly: true,
			Schema: hostcore.Schema(""), Call: r.toolProjects},
	}
}

func (r *aiReviewRun) toolProjects(ctx context.Context, _ json.RawMessage) (string, error) {
	se, err := r.x.st.Session(r.session)
	if err != nil {
		return "", err
	}
	return hostcore.ProjectsTool(ctx, se)
}

func (r *aiReviewRun) toolFinding(_ context.Context, raw json.RawMessage) (string, error) {
	var in rawFinding
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", err
	}
	f, ok := in.finding()
	if !ok {
		return "", errors.New("a finding needs a title")
	}
	if !r.record([]core.Finding{f}) {
		return "already reported", nil
	}
	return "recorded", nil
}

// record keeps findings the session hasn't got already; it reports whether it kept any.
func (r *aiReviewRun) record(fs []core.Finding) bool {
	se, err := r.x.st.Session(r.session)
	if err != nil {
		return false
	}
	var keep []core.Finding
	for _, f := range fs {
		if slices.ContainsFunc(append(slices.Clone(se.Findings), keep...), func(o core.Finding) bool { return sameFinding(o, f) }) {
			continue
		}
		f.ID, f.Review = hostcore.NewID(4), r.review.ID
		if f.Project == "" && len(se.Projects) > 0 {
			if p, ok := se.Primary(); ok {
				f.Project = p.Name
			}
		}
		keep = append(keep, f)
	}
	if len(keep) == 0 {
		return false
	}
	if r.x.st.Emit(core.FindingsRecorded{Session: r.session, Findings: keep}) != nil {
		return false
	}
	r.mu.Lock()
	r.found += len(keep)
	r.mu.Unlock()
	return true
}

func sameFinding(a, b core.Finding) bool {
	return a.File == b.File && strings.EqualFold(strings.TrimSpace(a.Title), strings.TrimSpace(b.Title)) && (a.Project == b.Project || b.Project == "")
}

// run starts the reviewer, answers it until its turn ends, and records how the review went.
func (r *aiReviewRun) run(p agent.Provider, spec agent.StartSpec) {
	ctx, cancel := context.WithTimeout(context.Background(), AIReviewTimeout)
	defer cancel()
	go func() {
		select {
		case <-r.x.env.Closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	var said []string
	errText := ""
	sess, err := p.Start(ctx, spec)
	if err != nil {
		errText = err.Error()
	} else {
		said, errText = r.converse(ctx, sess)
		_ = sess.Close()
	}
	r.mu.Lock()
	found := r.found
	r.mu.Unlock()
	if found == 0 {
		for _, text := range slices.Backward(said) {
			if fs := ParseFindings(text); len(fs) > 0 {
				r.record(fs)
				break
			}
		}
	}
	r.end(said, errText)
}

// end records the review as over: its verdict is the last thing the reviewer said.
func (r *aiReviewRun) end(said []string, errText string) {
	rv := r.review
	rv.Running, rv.Ended, rv.Error = false, r.x.env.Now().UTC(), errText
	if len(said) > 0 {
		rv.Verdict = verdict(said[len(said)-1])
	}
	_ = r.x.st.Emit(core.AIReviewChanged{Session: r.session, Review: rv})
}

// converse answers the reviewer until its turn ends: every permission is refused, since it only reads. It returns
// what the reviewer said, and why the review failed if it did.
func (r *aiReviewRun) converse(ctx context.Context, sess agent.Session) ([]string, string) {
	var said []string
	failed := ""
	for {
		select {
		case <-ctx.Done():
			select {
			case <-r.x.env.Closed: // closing ends ctx too
				return said, "Oriel stopped during the review"
			default:
			}
			return said, "the reviewer took too long"
		case <-r.x.env.Closed:
			return said, "Oriel stopped during the review"
		case ev, ok := <-sess.Events():
			if !ok {
				return said, "the reviewer exited before it finished"
			}
			switch ev := ev.(type) {
			case agent.Message:
				if !ev.Partial && ev.Agent == "" && (ev.Role == "" || ev.Role == core.ItemAssistant) && strings.TrimSpace(ev.Text) != "" {
					said = append(said, ev.Text)
				}
			case agent.Permission:
				_ = sess.Answer(ctx, ev.ID, agent.Decision{Note: "The AI review only reads: read the files instead, and change nothing."})
			case agent.Question:
				_ = sess.Answer(ctx, ev.ID, agent.Decision{Note: "Nobody answers questions during an AI review: decide for yourself, and say so in the finding."})
			case agent.Failure:
				failed = ev.Err
			case agent.AuthExpired:
				return said, ev.Error()
			case agent.Exited:
				return said, "the reviewer exited before it finished"
			case agent.TurnEnd:
				return said, failed
			}
		}
	}
}

// verdict is the last line the reviewer said, outside any code block.
func verdict(text string) string {
	text = fenced.ReplaceAllString(text, "")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for _, l := range slices.Backward(lines) {
		if l = strings.TrimSpace(l); l != "" {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return ""
}
