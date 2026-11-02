package agentrun

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/core"
)

// The agent reads a batch as tagged text, crit's format: tags mark where each thread and comment begins and
// ends, and bodies stay raw markdown inside them.

const (
	clipLines = 40
	clipKeep  = 12
)

// BatchLead opens every batch of comments the agent is given.
const BatchLead = "New Oriel comments. Handle them as your skill says, answering with the mcp__oriel__* tools, then end your turn."

// NoticeLead opens a batch of Oriel's notices alone, with no comments: nothing the user wrote.
const NoticeLead = "A notice from Oriel. Act on it as your skill says."

// renderBatch is the prompt for threads with comments the agent hasn't had: the new ones marked, the rest of
// each thread for context.
func renderBatch(se core.SessionState, threads []core.Thread, fresh map[string]bool, notes []string) string {
	var b strings.Builder
	if len(threads) == 0 {
		b.WriteString(NoticeLead + "\n\n")
	} else {
		b.WriteString(BatchLead + "\n\n")
	}
	for _, t := range threads {
		if hidden(t) {
			continue
		}
		renderThread(&b, se, t, fresh, false)
		b.WriteString("\n")
	}
	for _, n := range notes {
		fmt.Fprintf(&b, "<notice>%s</notice>\n", escapeBody(n))
	}
	open := 0
	for _, t := range se.Threads {
		if t.Open() && !t.Btw && !hidden(t) {
			open++
		}
	}
	if len(threads) > 0 {
		fmt.Fprintf(&b, "\n%d open thread%s in all.\n", open, plural(open))
	}
	return strings.TrimSpace(b.String())
}

// hidden says the agent isn't told of t at all: every comment in it is an untrusted author's.
func hidden(t core.Thread) bool {
	return len(t.Comments) > 0 && !slices.ContainsFunc(t.Comments, func(c core.Comment) bool { return !c.Redacted })
}

// renderThreadFull is one thread, nothing clipped: the thread tool's answer.
func renderThreadFull(se core.SessionState, t core.Thread) string {
	var b strings.Builder
	renderThread(&b, se, t, nil, true)
	return strings.TrimSpace(b.String())
}

func renderThread(b *strings.Builder, se core.SessionState, t core.Thread, fresh map[string]bool, full bool) {
	attrs := [][2]string{{"id", string(t.ID)}, {"where", where(se, t)}}
	if a := t.Anchor; a != nil && a.Side == "old" {
		attrs = append(attrs, [2]string{"side", "old"})
	}
	if len(se.Projects) > 1 && t.Anchor != nil && t.Anchor.Project != "" && t.Anchor.Project != core.DocsProject {
		attrs = append(attrs, [2]string{"project", t.Anchor.Project})
	}
	if t.Status != core.ThreadOpen && t.Status != "" {
		attrs = append(attrs, [2]string{"status", string(t.Status)})
	}
	if t.Urgent {
		attrs = append(attrs, [2]string{"urgent", "true"})
	}
	origin := t.Origin
	if t.Hosted != nil && t.Hosted.Host != "" {
		origin = t.Hosted.Host
	}
	if origin != "" {
		attrs = append(attrs, [2]string{"origin", origin})
	}
	b.WriteString(openTag("thread", attrs) + "\n")
	if t.Anchor != nil && strings.TrimSpace(t.Anchor.Text) != "" {
		fmt.Fprintf(b, "<anchor>%s</anchor>\n", escapeBody(strings.TrimSpace(t.Anchor.Text)))
	}
	for _, c := range t.Comments {
		if c.Btw || c.Redacted {
			continue
		}
		renderComment(b, c, origin, fresh[c.ID], full)
	}
	b.WriteString("</thread>\n")
}

func renderComment(b *strings.Builder, c core.Comment, origin string, isNew, full bool) {
	by := "user"
	if c.Author == core.AuthorAgent {
		by = "you"
	} else if c.Author == core.AuthorHost {
		by = c.Name
	}
	attrs := [][2]string{{"by", by}}
	if c.Author == core.AuthorHost {
		attrs = append(attrs, [2]string{"origin", origin})
	}
	if c.Draft {
		attrs = append(attrs, [2]string{"draft", "true"})
	} else if c.Author == core.AuthorAgent && !c.Sent.IsZero() {
		attrs = append(attrs, [2]string{"forwarded", "true"})
	}
	if c.Edited {
		attrs = append(attrs, [2]string{"edited", "true"})
	}
	if isNew {
		attrs = append(attrs, [2]string{"new", "true"})
	}
	if c.Progress {
		attrs = append(attrs, [2]string{"progress", "true"})
	}
	body := strings.TrimSpace(c.Body)
	if !full && c.Author == core.AuthorAgent {
		body = clip(body)
	}
	if body == "" && len(c.Images) == 0 {
		b.WriteString(strings.TrimSuffix(openTag("comment", attrs), ">") + "/>\n")
		return
	}
	b.WriteString(openTag("comment", attrs) + "\n")
	if body != "" {
		b.WriteString(escapeBody(body) + "\n")
	}
	for _, img := range c.Images {
		fmt.Fprintf(b, "<image path=%s/>\n", attr(img))
	}
	b.WriteString("</comment>\n")
}

func clip(body string) string {
	lines := strings.Split(body, "\n")
	if len(lines) <= clipLines {
		return body
	}
	return strings.Join(lines[:clipKeep], "\n") +
		fmt.Sprintf("\n… (%d more lines clipped: mcp__oriel__thread for the full text)", len(lines)-clipKeep)
}

// where is a thread's place: file:line, or the whole change; a plan's own thread is "plan", and a plan doc's lines
// are relative to the workspace.
func where(se core.SessionState, t core.Thread) string {
	a := t.Anchor
	if a == nil {
		if se.StartWork && t.ID == se.PlanThread() {
			return "plan"
		}
		return "general"
	}
	if a.Line <= 0 {
		return a.File
	}
	end := max(a.EndLine, a.Line)
	switch {
	case a.Col > 0 && a.EndCol > 0:
		return fmt.Sprintf("%s:%d:%d-%d:%d", a.File, a.Line, a.Col, end, a.EndCol)
	case a.Col > 0:
		return fmt.Sprintf("%s:%d:%d", a.File, a.Line, a.Col)
	case a.EndLine > a.Line:
		return fmt.Sprintf("%s:%d-%d", a.File, a.Line, a.EndLine)
	}
	return a.File + ":" + strconv.Itoa(a.Line)
}

func openTag(name string, attrs [][2]string) string {
	var b strings.Builder
	b.WriteString("<" + name)
	for _, a := range attrs {
		b.WriteString(" " + a[0] + "=" + attr(a[1]))
	}
	b.WriteString(">")
	return b.String()
}

func attr(v string) string {
	return `"` + strings.NewReplacer(`&`, "&amp;", `"`, "&quot;", "\n", " ").Replace(v) + `"`
}

var closingTag = regexp.MustCompile(`</(thread|comment|anchor|image|note|notice)\b`)

// escapeBody stops a body from closing one of the batch's own tags.
func escapeBody(s string) string { return closingTag.ReplaceAllString(s, `<\/$1`) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
