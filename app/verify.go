package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// Verify (agents.md, ui.md 13): when you choose to, a tool-less fork of the session's agent asks you about the change,
// one question at a time, and grades each answer. It never holds a raise. It's built on the public pieces:
// registry.Fork asks, a ShipGate shows the result and starts it, a ShipRaise rewrite adds a passed quiz's answers to
// the PR as reviewer notes, and the result is a session event.

// VerifyKeyMode is the quiz dialog's keymap mode.
const VerifyKeyMode = "verify"

const (
	verifyHelp    = "git/verify"
	quizLength    = 4
	verifyTimeout = 5 * time.Minute
)

// The reviewer notes a passed quiz adds to a PR's body, between these markers.
const (
	notesStart = "<!-- oriel:verify -->"
	notesEnd   = "<!-- /oriel:verify -->"
)

type verifyPlugin struct {
	disposer
	sh     *shell
	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc
	busy   sync.WaitGroup

	// On the loop.
	quizzes map[core.SessionID]*quiz
	open    core.SessionID // the session whose quiz the dialog shows
}

// quiz is a session's quiz as it goes.
type quiz struct {
	session  core.SessionID
	projects []string
	by       string
	qs       []core.QuizQuestion
	at       int  // the question being asked; those before it are graded
	asking   bool // waiting on the fork
	err      string
	answer   string // the composer
	notes    bool   // the answers go in the PR as reviewer notes
	result   string // once every question is graded
	gen      int
	scroll   scrollState
	focus    bool
}

func (x *verifyPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "verify"} }

func (x *verifyPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.quizzes = map[core.SessionID]*quiz{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.add(registry.On(h, registry.Hook{Kind: registry.Rewrite, Label: "verify notes"}, x.beforeRaise))
	x.add(h.Contribute(registry.ShipGate{Name: "verify", Title: "Verify quiz", Order: 50, Check: x.gate}))
	open := func(registry.Args) bool { return x.shown() != nil }
	for _, c := range []registry.Command{
		{ID: "verify.open", Title: "Take the verify quiz on the change of the session in front: {id}",
			Available: func(a registry.Args) bool { _, ok := x.session(a); return ok && len(registry.Forkers(h)) > 0 },
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, _ := x.session(a)
				return x.begin(se)
			}},
		{ID: "verify.send", Title: "Send your answer to the quiz's question: {text}, else what's in the composer", Available: open,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.send(x.shown(), a.String("text")) }},
		{ID: "verify.again", Title: "Take the verify quiz again, with new questions", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.again(x.shown()) }},
		{ID: "verify.open-file", Title: "Open the file the quiz is asking about", Available: open,
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.openFile(ctx, x.shown()) }},
		{ID: "verify.close", Title: "Close the verify quiz; it waits where it was", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.open = ""; return nil }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: VerifyKeyMode, Keys: "<D-CR>", Command: "verify.send", Desc: "Send the answer"}))
	x.add(h.Keymap(registry.Keymap{Mode: VerifyKeyMode, Keys: "<M-o>", Command: "verify.open-file", Desc: "Open the file it's asking about"}))
	x.add(h.Keymap(registry.Keymap{Mode: VerifyKeyMode, Keys: "<Esc>", Command: "verify.close", Desc: "Close the quiz"}))
	x.add(h.View(OverlayPrefix+"verify", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "verify", Fn: x.dialog}
	})))
	return nil
}

func (x *verifyPlugin) Deactivate(ctx context.Context) error {
	x.cancel()
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

// wait is until every question asked of the fork has its answer.
func (x *verifyPlugin) wait() { x.busy.Wait() }

// shown is the quiz the dialog shows, nil while it's closed.
func (x *verifyPlugin) shown() *quiz {
	if x.open == "" {
		return nil
	}
	return x.quizzes[x.open]
}

// session is {id}, else the session in front.
func (x *verifyPlugin) session(a registry.Args) (core.SessionState, bool) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = x.h.State().Focus
	}
	se, ok := x.h.Session(id)
	if !ok {
		return core.SessionState{}, false
	}
	return se.Snapshot(), true
}

// raisable are the session's projects that can have a PR.
func raisable(se core.SessionState) []core.Project {
	var out []core.Project
	for _, p := range se.Projects {
		if !p.ReadOnly && !p.Detached {
			out = append(out, p)
		}
	}
	return out
}

// result is how a session's quiz went: this run's, else the last one recorded; "" for none yet.
func (x *verifyPlugin) result(se core.SessionState) (string, *quiz) {
	if q := x.quizzes[se.ID]; q != nil {
		return q.result, q
	}
	if v, ok := se.Verification(); ok {
		return v.Result, nil
	}
	return "", nil
}

// beforeRaise adds a passed quiz's answers to a PR it raises, as reviewer notes, when they're wanted.
func (x *verifyPlugin) beforeRaise(_ registry.Ctx, ev *ShipRaise) (registry.Verdict, error) {
	if ev.Edit {
		return registry.Verdict{}, nil
	}
	sess, ok := x.h.Session(core.SessionID(ev.Session))
	if !ok {
		return registry.Verdict{}, nil
	}
	res, q := x.result(sess.Snapshot())
	if res != core.VerifyPassed || q == nil || !q.notes || strings.Contains(ev.Body, notesStart) {
		return registry.Verdict{}, nil
	}
	ev.Body = strings.TrimRight(ev.Body, "\n") + "\n\n" + reviewerNotes(q)
	return registry.Verdict{Rewrote: true}, nil
}

// begin opens the dialog on the session's quiz, starting one if there's none to go back to.
func (x *verifyPlugin) begin(se core.SessionState) error {
	if len(registry.Forkers(x.h)) == 0 {
		return errors.New("the quiz needs an agent to ask it")
	}
	q := x.quizzes[se.ID]
	if q == nil {
		q = &quiz{session: se.ID, notes: true}
		x.quizzes[se.ID] = q
		x.start(q, se)
	}
	q.focus = true
	x.open = se.ID
	return nil
}

// start asks the fork for the questions.
func (x *verifyPlugin) start(q *quiz, se core.SessionState) {
	q.projects = nil
	for _, p := range raisable(se) {
		q.projects = append(q.projects, p.Name)
	}
	q.by = cmpOr(registry.ForkLabel(x.h, string(se.ID)), "fork, no tools")
	q.qs, q.at, q.result, q.err, q.answer = nil, 0, "", "", ""
	x.ask(q, questionsPrompt(se), func(out string) error {
		qs := parseQuestions(out)
		if len(qs) == 0 {
			return errors.New("the agent's questions didn't come back in a form the quiz reads")
		}
		q.qs = qs
		return nil
	})
}

// ask puts a prompt to the fork off the loop, and gives the answer to got back on it.
func (x *verifyPlugin) ask(q *quiz, prompt string, got func(out string) error) {
	q.gen++
	gen := q.gen
	q.asking, q.err = true, ""
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(x.ctx, verifyTimeout)
		out, _, err := registry.Fork(ctx, x.h, registry.ForkRequest{Session: string(q.session), Prompt: prompt})
		cancel()
		x.h.Do(func() {
			if gen != q.gen {
				return
			}
			q.asking = false
			if err == nil {
				err = got(out)
			}
			if err != nil {
				q.err = err.Error()
			}
			q.scroll.follow = true
		})
		x.h.Redraw()
	}()
}

// send gives the answer to the question being asked, to be graded.
func (x *verifyPlugin) send(q *quiz, text string) error {
	if q == nil || q.asking || q.result != "" || q.at >= len(q.qs) {
		return nil
	}
	text = strings.TrimSpace(cmpOr(text, q.answer))
	if text == "" {
		return nil
	}
	sess, ok := x.h.Session(q.session)
	if !ok {
		return fmt.Errorf("no session %s", q.session)
	}
	i := q.at
	q.qs[i].Answer, q.answer = text, ""
	x.ask(q, gradePrompt(sess.Snapshot(), q.qs, i), func(out string) error {
		v := parseVerdict(out)
		q.qs[i].Verdict, q.qs[i].Note, q.qs[i].File, q.qs[i].Line = v.Verdict, v.Note, v.File, v.Line
		q.at = i + 1
		if q.at == len(q.qs) {
			res := core.VerifyPassed
			if (core.Verification{Questions: q.qs}).Count(core.VerdictWrong) > 0 {
				res = core.VerifyFailed
			}
			x.record(q, res)
		}
		return nil
	})
	return nil
}

// record keeps the quiz's result in the session, which logs it.
func (x *verifyPlugin) record(q *quiz, result string) {
	q.result = result
	v := core.Verification{Result: result, Projects: q.projects, By: q.by, Questions: append([]core.QuizQuestion(nil), q.qs[:q.at]...),
		At: time.Now()}
	log := x.h.Log().With("session", string(q.session))
	log.Info("verification "+result, "right", v.Count(core.VerdictRight), "partly", v.Count(core.VerdictPartly), "wrong", v.Count(core.VerdictWrong))
	if x.sh.sessions == nil {
		return
	}
	c, err := x.sh.sessions.client(q.session)
	if err != nil {
		log.Error("verification not recorded", "err", err)
		return
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(x.ctx, 30*time.Second)
		defer cancel()
		if err := c.RecordVerified(ctx, core.Verified{Session: q.session, Verification: v}); err != nil {
			log.Error("verification not recorded", "err", err)
		}
	}()
}

// again starts a new quiz in place of a finished one.
func (x *verifyPlugin) again(q *quiz) error {
	if q == nil {
		return nil
	}
	sess, ok := x.h.Session(q.session)
	if !ok {
		return fmt.Errorf("no session %s", q.session)
	}
	x.start(q, sess.Snapshot())
	return nil
}

// pointer is the file the quiz is asking about: the last answer's, when it wasn't right, else the question's.
func (q *quiz) pointer() (string, int) {
	if q.at > 0 && q.at <= len(q.qs) {
		if p := q.qs[q.at-1]; p.Verdict != core.VerdictRight && p.File != "" {
			return p.File, p.Line
		}
	}
	if q.at < len(q.qs) && q.qs[q.at].File != "" {
		return q.qs[q.at].File, q.qs[q.at].Line
	}
	for i := min(q.at, len(q.qs)) - 1; i >= 0; i-- {
		if p := q.qs[i]; p.Verdict != core.VerdictRight && p.File != "" {
			return p.File, p.Line
		}
	}
	return "", 0
}

// openFile opens the file the quiz is asking about; the quiz waits, closed, until it's opened again.
func (x *verifyPlugin) openFile(ctx registry.Ctx, q *quiz) error {
	if q == nil {
		return nil
	}
	f, line := q.pointer()
	if f == "" {
		return errors.New("the quiz isn't pointing at a file")
	}
	return x.openAt(ctx, q, f, line)
}

// openAt opens a file of the session's, the quiz waiting closed.
func (x *verifyPlugin) openAt(ctx registry.Ctx, q *quiz, f string, line int) error {
	sess, ok := x.h.Session(q.session)
	if !ok {
		return fmt.Errorf("no session %s", q.session)
	}
	x.open = ""
	a := registry.Args{"path": quizPath(sess.Snapshot(), f)}
	if line > 0 {
		a["line"] = line
	}
	return x.h.Run(ctx, "editor.open", a)
}

// quizPath is a file the fork named, in the session's projects: by a project's name before it, else the first
// project that has it.
func quizPath(se core.SessionState, f string) string {
	f = filepath.FromSlash(strings.TrimPrefix(f, "./"))
	if filepath.IsAbs(f) {
		return f
	}
	ps := raisable(se)
	for _, p := range ps {
		if rest, ok := strings.CutPrefix(f, p.Name+string(filepath.Separator)); ok {
			return filepath.Join(p.Dir, rest)
		}
	}
	for _, p := range ps {
		if _, err := os.Stat(filepath.Join(p.Dir, f)); err == nil {
			return filepath.Join(p.Dir, f)
		}
	}
	if len(ps) > 0 {
		return filepath.Join(ps[0].Dir, f)
	}
	return f
}

// gate is the "Verify quiz" row of "before this ships": up to you, and where to start it or go back to it.
func (x *verifyPlugin) gate(id string) registry.Gate {
	sess, ok := x.h.Session(core.SessionID(id))
	if !ok || len(raisable(sess.Snapshot())) == 0 {
		return registry.Gate{}
	}
	se := sess.Snapshot()
	open := registry.Args{"id": id}
	res, q := x.result(se)
	switch res {
	case core.VerifyPassed, core.VerifyFailed:
		v, _ := se.Verification()
		if q != nil {
			v = core.Verification{Questions: q.qs}
		}
		if res == core.VerifyFailed {
			return registry.Gate{State: registry.GateWarn, Note: fmt.Sprintf("%d wrong", v.Count(core.VerdictWrong)), Action: "Again",
				Command: "verify.open", Args: open}
		}
		return registry.Gate{State: registry.GatePass, Note: fmt.Sprintf("%d/%d right", v.Count(core.VerdictRight), len(v.Questions))}
	}
	if q != nil && len(q.qs) > 0 {
		return registry.Gate{State: registry.GatePending, Note: fmt.Sprintf("%d/%d", q.at, len(q.qs)), Action: "Resume",
			Command: "verify.open", Args: open}
	}
	return registry.Gate{State: registry.GateOptional, Note: "optional", Action: "Verify…", Command: "verify.open", Args: open}
}

// reviewerNotes are a passed quiz's questions and answers, for the PR's body.
func reviewerNotes(q *quiz) string {
	v := core.Verification{Questions: q.qs}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n### Reviewer notes\n\nVerified before raising (%s): %d of %d right", notesStart, q.by, v.Count(core.VerdictRight), len(q.qs))
	if n := v.Count(core.VerdictPartly); n > 0 {
		fmt.Fprintf(&b, ", %d partly", n)
	}
	b.WriteString(".\n")
	for _, qq := range q.qs {
		fmt.Fprintf(&b, "\n**%s**\n%s\n", qq.Question, qq.Answer)
		if qq.Note != "" {
			fmt.Fprintf(&b, "%s %s\n", verdictGlyph(qq.Verdict), qq.Note)
		}
	}
	b.WriteString(notesEnd + "\n")
	return b.String()
}

func verdictGlyph(v string) string {
	switch v {
	case core.VerdictRight:
		return "✓"
	case core.VerdictPartly:
		return "◐"
	case core.VerdictWrong:
		return "✕"
	}
	return ""
}

// questionsPrompt asks the fork for the quiz's questions.
func questionsPrompt(se core.SessionState) string {
	var b strings.Builder
	b.WriteString("This is Oriel's verify quiz. The user is about to raise the pull request for this session's change and put their name on it; " +
		"check they understand it. You have no tools here, and this exchange is not part of the main conversation.\n\nThe change:\n")
	for _, p := range raisable(se) {
		fmt.Fprintf(&b, "- %s: branch %s against %s\n", p.Name, cmpOr(p.Branch, se.Branch), cmpOr(p.Base, "its base"))
	}
	fmt.Fprintf(&b, "\nWrite %d short questions about the diff, each answerable in a sentence or two by someone who has read it: what a case does now, "+
		"which line decides it, what happens to existing data. Put names and values in `inline code`.\n\n"+
		"Reply with JSON only: {\"questions\": [{\"question\": \"...\", \"file\": \"path/in/its/project\", \"line\": 42}]}. "+
		"file and line are where the answer is, when one place has it.", quizLength)
	return b.String()
}

// gradePrompt asks the fork to grade the answer to question i.
func gradePrompt(se core.SessionState, qs []core.QuizQuestion, i int) string {
	var b strings.Builder
	b.WriteString("This is Oriel's verify quiz, about the change this session is about to raise as a pull request. You have no tools here, " +
		"and this exchange is not part of the main conversation. Grade the user's answer against the change as it is.\n\n")
	fmt.Fprintf(&b, "Question %d: %s\n", i+1, qs[i].Question)
	if qs[i].File != "" {
		fmt.Fprintf(&b, "(the answer is in %s)\n", qs[i].File)
	}
	fmt.Fprintf(&b, "Their answer: %s\n\n", qs[i].Answer)
	b.WriteString("Reply with JSON only: {\"verdict\": \"right\" | \"partly\" | \"wrong\", \"note\": \"a sentence or two: what they got right, or missed\", " +
		"\"file\": \"where to read, for partly or wrong\", \"line\": 0}.")
	return b.String()
}

// jsonObject is the JSON object in a reply: inside a code fence, or from its first { to its last }.
func jsonObject(s string) string {
	i, j := strings.IndexByte(s, '{'), strings.LastIndexByte(s, '}')
	if i < 0 || j < i {
		return ""
	}
	return s[i : j+1]
}

// lineNumber reads a line given as a number or a string.
type lineNumber int

func (n *lineNumber) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	v, err := strconv.Atoi(s)
	if err != nil {
		*n = 0
		return nil
	}
	*n = lineNumber(v)
	return nil
}

// parseQuestions reads the fork's questions: JSON as asked, else numbered or bulleted lines, else lines ending "?".
func parseQuestions(s string) []core.QuizQuestion {
	var out []core.QuizQuestion
	var r struct {
		Questions []struct {
			Question string     `json:"question"`
			Q        string     `json:"q"`
			Text     string     `json:"text"`
			File     string     `json:"file"`
			Line     lineNumber `json:"line"`
		} `json:"questions"`
	}
	if js := jsonObject(s); js != "" && json.Unmarshal([]byte(js), &r) == nil {
		for _, q := range r.Questions {
			text := strings.TrimSpace(cmpOr(q.Question, cmpOr(q.Q, q.Text)))
			if text != "" {
				f, l := q.File, int(q.Line)
				if f == "" {
					f, l = fileRef(text)
				}
				out = append(out, core.QuizQuestion{Question: text, File: f, Line: l})
			}
		}
	}
	if len(out) == 0 {
		for _, l := range strings.Split(s, "\n") {
			l = strings.TrimSpace(l)
			m := quizItem.FindStringSubmatch(l)
			switch {
			case m != nil:
				l = strings.TrimSpace(m[1])
			case !strings.HasSuffix(l, "?"):
				continue
			}
			if l != "" {
				f, n := fileRef(l)
				out = append(out, core.QuizQuestion{Question: l, File: f, Line: n})
			}
		}
	}
	if len(out) > quizLength {
		out = out[:quizLength]
	}
	return out
}

var (
	quizItem = regexp.MustCompile(`^(?:\d+[.)]|[-*•])\s+(.+)$`)
	fileLine = regexp.MustCompile("`?([\\w./-]+\\.[A-Za-z0-9]+)(?::(\\d+))?`?")
)

// fileRef is the first file named in text, with its line when it says one: "round.go:42", "0042.sql".
func fileRef(text string) (string, int) {
	for _, m := range fileLine.FindAllStringSubmatch(text, -1) {
		name := strings.Trim(m[1], ".-")
		if _, err := strconv.ParseFloat(name, 64); err == nil || len(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))) < 2 {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		return name, n
	}
	return "", 0
}

type verdict struct {
	Verdict string
	Note    string
	File    string
	Line    int
}

// parseVerdict reads the fork's grade: JSON as asked, else a verdict word or glyph at its start. Anything it can't
// read is partly right, with what it said as the note.
func parseVerdict(s string) verdict {
	var r struct {
		Verdict string     `json:"verdict"`
		Note    string     `json:"note"`
		File    string     `json:"file"`
		Line    lineNumber `json:"line"`
	}
	var v verdict
	if js := jsonObject(s); js != "" && json.Unmarshal([]byte(js), &r) == nil && r.Verdict != "" {
		v = verdict{Verdict: verdictWord(r.Verdict), Note: strings.TrimSpace(r.Note), File: r.File, Line: int(r.Line)}
	} else {
		text := strings.TrimSpace(s)
		v = verdict{Verdict: verdictWord(text), Note: strings.TrimSpace(strings.TrimLeft(text, "✓✔◐✕✗ "))}
	}
	if v.Verdict == "" {
		v.Verdict = core.VerdictPartly
	}
	if v.File == "" && v.Verdict != core.VerdictRight {
		v.File, v.Line = fileRef(v.Note)
	}
	return v
}

// verdictWord is the verdict a word or glyph at the start of s gives, "" for none.
func verdictWord(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, w := range []struct{ prefix, verdict string }{
		{"✓", core.VerdictRight}, {"✔", core.VerdictRight}, {"right", core.VerdictRight}, {"correct", core.VerdictRight}, {"yes", core.VerdictRight},
		{"◐", core.VerdictPartly}, {"partly", core.VerdictPartly}, {"partial", core.VerdictPartly}, {"mostly", core.VerdictPartly},
		{"✕", core.VerdictWrong}, {"✗", core.VerdictWrong}, {"wrong", core.VerdictWrong}, {"incorrect", core.VerdictWrong}, {"no", core.VerdictWrong},
	} {
		rest, ok := strings.CutPrefix(s, w.prefix)
		if r, _ := utf8.DecodeRuneInString(rest); ok && !unicode.IsLetter(r) {
			return w.verdict
		}
	}
	return ""
}
