package agentui

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tAskLead   = theme.Declare("ask.prompt.lead", "cyan")      // a command's "$", an argument's name
	tAskAddFg  = theme.Declare("ask.prompt.diff.add", "green") // an edit's added line's gutter
	tAskDelFg  = theme.Declare("ask.prompt.diff.del", "red")
	tAskGutter = theme.Declare("ask.prompt.diff.gutter", "fg.ghost")
)

// forAll is ask.answer's {for} for an always that makes a rule every session meets.
const forAll = "all"

// decision is an ask that holds the agent until you allow or deny it (asksYou), as its prompt and its card show it:
// what it is, where, why, its detail, and its answers.
type decision struct {
	title, where, why string
	code              string // the command, for a card that shows it in a code box
	detail            []widget.PromptLine
	detailLabel       string
	scopes            []string // a network ask's: the host, and its domain
	scopeArgs         []string // the {scope} each makes
	options           []decisionOption
}

// decisionOption is one of a decision's answers, and what it does; scoped takes the scope chosen.
type decisionOption struct {
	widget.PromptOption
	act    askAct
	scoped bool
}

// decide is ask a as a decision in session se.
func (x *Console) decide(th *theme.Resolved, se core.SessionState, a core.Ask) decision {
	prov := kit.ProviderName(x.h, se.Agent.Provider)
	d := decision{why: askWhy(se, a)}
	label := kit.Label(se)
	switch a.Kind {
	case core.AskNetwork:
		host, port, err := net.SplitHostPort(a.Host)
		if err != nil {
			host = a.Host
		}
		d.title = "The agent is trying to connect to " + host
		d.where = kit.JoinParts(label, a.Detail)
		if port != "" {
			d.where = kit.JoinParts(label, "CONNECT :"+port)
		}
		d.scopes, d.scopeArgs = []string{host}, []string{a.Host}
		if domain, _ := domainOf(a.Host); host != domain && domain != "" {
			scope := "*." + domain
			if port != "" {
				scope += ":" + port
			}
			d.scopes, d.scopeArgs = append(d.scopes, "*."+domain), append(d.scopeArgs, scope)
		}
		d.options = []decisionOption{
			{PromptOption: widget.PromptOption{Key: "o", Label: "Once"}, act: askAct{args: registry.Args{"decision": "allow"}}},
			{PromptOption: widget.PromptOption{Key: "a", Label: "Allow for this session"}, act: askAct{args: registry.Args{"decision": "always"}}, scoped: true},
			{PromptOption: widget.PromptOption{Key: "A", Label: "Allow for all sessions", Note: "every session"},
				act: askAct{args: registry.Args{"decision": "always", "for": forAll}}, scoped: true},
			{PromptOption: widget.PromptOption{Key: "n", Label: "Deny", Danger: true}, act: askAct{args: registry.Args{"decision": "deny"}}},
		}
		return d
	case core.AskGateway:
		d.title = prov + " wants to use " + toolTitle(a.Tool) + " through the gateway"
		if own, ok := strings.CutPrefix(a.Tool, "oriel__"); ok { // the gateway's own: a push, a pull request
			d.title = prov + " wants to " + strings.ReplaceAll(own, "_", " ") + " through the gateway"
		}
		d.where = kit.JoinParts(label, "the sandbox's gateway", a.Detail)
		d.detail, d.detailLabel = argLines(th, a.Input), "Its arguments"
		if len(d.detail) == 0 && a.Summary != "" {
			d.detail = []widget.PromptLine{{Text: a.Summary}}
		}
	default:
		x.permissionDecision(th, &d, se, a, prov)
	}
	d.options = answers(a.Rule)
	return d
}

// permissionDecision is an agent's own gate asking: a command, an edit, a tool (an MCP server's, a connector's).
func (x *Console) permissionDecision(th *theme.Resolved, d *decision, se core.SessionState, a core.Ask, prov string) {
	label := kit.Label(se)
	in := hostArgs(a.Input)
	switch {
	case core.ShellTool(a.Tool):
		cmd := cmp.Or(in["command"], a.Summary, kit.MainInput(a.Input))
		d.title, d.code, d.detailLabel = prov+" wants to run a command", "$ "+cmd, "The command"
		where := ""
		if p, ok := se.Primary(); ok {
			where = kit.TildePath(cmp.Or(in["cwd"], p.Dir))
		}
		d.where = kit.JoinParts(label, se.Branch, where, map[bool]string{true: "sandbox", false: "on this machine"}[se.Agent.Sandboxed])
		for i, l := range strings.Split(cmd, "\n") {
			lead := "  "
			if i == 0 {
				lead = "$ "
			}
			d.detail = append(d.detail, widget.PromptLine{Lead: lead, Text: l, LeadTone: th.C(tAskLead)})
		}
	case a.Diff != nil:
		verb := "edit"
		if a.Tool == "Write" {
			verb = "write"
		}
		d.title, d.detailLabel = prov+" wants to "+verb+" "+relPath(se, a.Diff.Path), "The edit"
		d.where = kit.JoinParts(label, se.Branch, diffSpan(a.Diff))
		d.detail = diffLines(th, a.Diff)
	default:
		server, tool, mcp := mcpTool(a.Tool)
		d.title = prov + " wants to use " + a.Tool
		d.where = kit.JoinParts(label, se.Branch)
		if mcp {
			d.title = prov + " wants to use " + server + ": " + tool
			d.where = kit.JoinParts(label, "MCP server "+server)
			if connector(a.Tool) {
				d.where = kit.JoinParts(label, "claude.ai connector · acts as you")
			}
		}
		if p := in["file_path"]; p != "" && !mcp {
			d.title = prov + " wants to use " + a.Tool + " on " + relPath(se, p)
		}
		d.detail, d.detailLabel = argLines(th, a.Input), "Its arguments"
		if len(d.detail) == 0 && a.Summary != "" {
			d.detail = []widget.PromptLine{{Text: a.Summary}}
		}
	}
}

// answers are a permission's or a gateway call's: once, for the session or for all sessions (with the rule each
// makes; none without a rule), deny, deny with a note.
func answers(rule string) []decisionOption {
	out := []decisionOption{{PromptOption: widget.PromptOption{Key: "y", Label: "Allow once"}, act: askAct{args: registry.Args{"decision": "allow"}}}}
	if rule != "" {
		out = append(out,
			decisionOption{PromptOption: widget.PromptOption{Key: "a", Label: "Allow for this session", Note: rule},
				act: askAct{args: registry.Args{"decision": "always"}}},
			decisionOption{PromptOption: widget.PromptOption{Key: "A", Label: "Allow for all sessions", Note: rule + " · every session"},
				act: askAct{args: registry.Args{"decision": "always", "for": forAll}}})
	}
	return append(out,
		decisionOption{PromptOption: widget.PromptOption{Key: "n", Label: "Deny", Danger: true}, act: askAct{args: registry.Args{"decision": "deny"}}},
		decisionOption{PromptOption: widget.PromptOption{Key: "N", Label: "Deny with a note…", Danger: true}, act: askAct{note: true}})
}

// promptCard is an ask that holds the agent, up in the middle of the window as a prompt.
func (x *Console) promptCard(c *ui.Ctx, key string, se core.SessionState, a core.Ask, p uireg.CardPlace) {
	th := c.Theme()
	st := x.state(se.ID)
	d := x.decide(th, se, a)
	scope := st.scope[a.ID]
	o := widget.PromptCardOpts{Kicker: kit.Label(se) + " · " + string(a.Kind), Title: d.title, Where: d.where, Why: d.why, Tone: th.C(askTone(a.Kind)),
		Detail: d.detail, DetailLabel: d.detailLabel, Help: "agent/asks#" + string(a.Kind), Dismiss: p.Dismiss != nil, TakeFocus: p.TakeFocus,
		Label: d.title}
	if p.Of > 0 {
		o.Count = fmt.Sprintf("%d of %d", p.N, p.Of)
	}
	if len(d.scopes) > 1 {
		o.Scopes, o.Scope = d.scopes, &scope
	}
	for _, opt := range d.options {
		po := opt.PromptOption
		if opt.scoped && len(d.scopes) > 0 {
			po.Note = strings.TrimSuffix(d.scopes[min(scope, len(d.scopes)-1)]+" · "+po.Note, " · ")
		}
		o.Options = append(o.Options, po)
	}
	o.Actions = []widget.CardAction{{Label: "Console", Key: "g"}} // the session's console, where it waits with what led to it
	if st.noting == a.ID && st.notingOn {
		o.Foot = func(c *ui.Ctx) { x.noteInput(c, se, a, st) }
	}
	res := widget.PromptCard(c, key, o)
	st.scope[a.ID] = scope
	switch {
	case res.Dismissed && p.Dismiss != nil:
		p.Dismiss()
	case res.Action == 0:
		if p.Dismiss != nil {
			p.Dismiss() // it steps aside for the console it takes you to, where it waits to be answered
		}
		x.run(context.Background(), "agent.console.show", registry.Args{"id": string(se.ID)})
	case res.Option >= 0:
		x.pick(se, a, d, d.options[res.Option], scope, true)
	}
}

// pick answers a with opt: a note to write first, or ask.answer with its args and the scope chosen.
func (x *Console) pick(se core.SessionState, a core.Ask, d decision, opt decisionOption, scope int, stacked bool) {
	if opt.act.note {
		x.startNote(se.ID, a.ID, stacked)
		return
	}
	args := registry.Args{"id": string(se.ID), "ask": a.ID}
	for n, v := range opt.act.args {
		args[n] = v
	}
	if opt.scoped && len(d.scopeArgs) > 0 {
		args["scope"] = d.scopeArgs[min(scope, len(d.scopeArgs)-1)]
	}
	x.run(context.Background(), "ask.answer", args)
}

// askWhy is what the agent last said before it asked, in its own turn: its last words in the main loop's transcript.
func askWhy(se core.SessionState, a core.Ask) string {
	for i := len(se.Transcript) - 1; i >= 0; i-- {
		it := se.Transcript[i]
		if it.Agent != "" || !a.Created.IsZero() && it.Time.After(a.Created) {
			continue
		}
		switch it.Kind {
		case core.ItemUser:
			return ""
		case core.ItemAssistant:
			if t := strings.TrimSpace(strings.ReplaceAll(it.Text, "`", "")); t != "" { // its words, not its markdown
				return t
			}
		}
	}
	return ""
}

// hostArgs are a tool call's string arguments, by name.
func hostArgs(raw string) map[string]string {
	var in map[string]any
	out := map[string]string{}
	if json.Unmarshal([]byte(raw), &in) != nil {
		return out
	}
	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// argLines are a call's arguments laid out a line each, name then value, in the order given: a long or nested value
// under its name, indented.
func argLines(th *theme.Resolved, raw string) []widget.PromptLine {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" || raw == "null" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return []widget.PromptLine{{Text: raw}}
	}
	type arg struct {
		name string
		v    json.RawMessage
	}
	var args []arg
	width := 0
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return []widget.PromptLine{{Text: raw}}
		}
		name, _ := tok.(string)
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return []widget.PromptLine{{Text: raw}}
		}
		args = append(args, arg{name, v})
		width = max(width, min(len([]rune(name)), 14))
	}
	key := th.C(tAskLead)
	var out []widget.PromptLine
	under := func(name string, lines []string) {
		out = append(out, widget.PromptLine{Lead: name, LeadTone: key})
		for _, l := range lines {
			out = append(out, widget.PromptLine{Lead: "  ", Text: l})
		}
	}
	for _, a := range args {
		pad := a.name + strings.Repeat(" ", max(width-len([]rune(a.name)), 0)+2)
		var s string
		switch {
		case json.Unmarshal(a.v, &s) == nil && strings.Contains(s, "\n"):
			under(a.name, strings.Split(strings.TrimRight(s, "\n"), "\n"))
		case json.Unmarshal(a.v, &s) == nil:
			out = append(out, widget.PromptLine{Lead: pad, Text: s, LeadTone: key})
		case len(a.v) > 2 && (a.v[0] == '{' || a.v[0] == '[') && len(a.v) <= 60 && !bytes.Contains(a.v[1:len(a.v)-1], []byte{'{'}):
			var b bytes.Buffer
			_ = json.Compact(&b, a.v)
			out = append(out, widget.PromptLine{Lead: pad, Text: strings.ReplaceAll(b.String(), `","`, `", "`), LeadTone: key})
		case len(a.v) > 2 && (a.v[0] == '{' || a.v[0] == '['):
			var b bytes.Buffer
			if err := json.Indent(&b, a.v, "", "  "); err != nil {
				out = append(out, widget.PromptLine{Lead: pad, Text: string(a.v), LeadTone: key})
				continue
			}
			under(a.name, strings.Split(b.String(), "\n"))
		default:
			out = append(out, widget.PromptLine{Lead: pad, Text: string(a.v), LeadTone: key})
		}
	}
	return out
}

// diffLines are an edit's lines for a prompt: the op and the line's number in its colour, the text, on the diff's
// colours.
func diffLines(th *theme.Resolved, d *core.FileDiff) []widget.PromptLine {
	out := make([]widget.PromptLine, 0, len(d.Lines))
	for _, l := range d.Lines {
		num, lead := l.New, th.C(tAskGutter)
		line := widget.PromptLine{Text: strings.ReplaceAll(l.Text, "\t", "    ")}
		switch l.Op {
		case "+":
			lead, line.Fill = th.C(tAskAddFg), th.C(theme.DiffAdd)
		case "-":
			num, lead, line.Fill = l.Old, th.C(tAskDelFg), th.C(theme.DiffDel)
		}
		n := ""
		if num > 0 {
			n = fmt.Sprint(num)
		}
		op := strings.TrimSpace(l.Op)
		if op == "" {
			op = " "
		}
		line.Lead, line.LeadTone = fmt.Sprintf("%s %4s  ", op, n), lead
		out = append(out, line)
	}
	return out
}

// diffSpan is the lines an edit touches, as the where line says it: "lines 46–48".
func diffSpan(d *core.FileDiff) string {
	lo, hi := 0, 0
	for _, l := range d.Lines {
		n := l.New
		if l.Op == "-" {
			n = l.Old
		}
		if l.Op == " " || n == 0 {
			continue
		}
		if lo == 0 || n < lo {
			lo = n
		}
		hi = max(hi, n)
	}
	switch {
	case lo == 0:
		return fmt.Sprintf("+%d −%d", d.Added, d.Removed)
	case lo == hi:
		return fmt.Sprintf("line %d", lo)
	}
	return fmt.Sprintf("lines %d–%d", lo, hi)
}

// relPath is path as its session's projects name it: relative to the project it's in.
func relPath(se core.SessionState, path string) string {
	for _, p := range se.Projects {
		if rel, err := filepath.Rel(p.Dir, path); err == nil && !strings.HasPrefix(rel, "..") && filepath.IsAbs(path) {
			return rel
		}
	}
	return kit.TildePath(path)
}

// mcpTool is an MCP server's tool, named as agents name it (mcp__server__tool): its server's name as you'd say it
// (a claude.ai connector's without its prefix) and the tool's.
func mcpTool(name string) (server, tool string, ok bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	if !ok {
		return "", "", false
	}
	server, tool, ok = strings.Cut(rest, "__")
	if !ok {
		return "", "", false
	}
	server = strings.TrimPrefix(server, "claude_ai_")
	return strings.ReplaceAll(server, "_", " "), tool, true
}

// connector says a tool is a claude.ai connector's, which acts as you.
func connector(name string) bool { return strings.HasPrefix(name, "mcp__claude_ai_") }

// toolTitle is a gateway's tool as you'd say it: "Linear: create_issue" for linear__create_issue.
func toolTitle(name string) string {
	if server, tool, ok := mcpTool(name); ok {
		return server + ": " + tool
	}
	if server, tool, ok := strings.Cut(name, "__"); ok {
		return server + ": " + tool
	}
	return name
}
