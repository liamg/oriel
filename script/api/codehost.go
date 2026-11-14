package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=code cap=ui
type CodeAPI struct{ a *APIs }

//oriel:api doc="A code host (GitLab, Azure DevOps…): where pull requests live. Oriel builds the Reviews page, the sidebar's pill, the notices and the assistant's review_requests on its review functions, and the Ship page's pull request step (raise, edit, ready, checks, merge) on the rest. { name, title, icon, order (50), words = { short ('PR', 'MR'), long ('pull request'), sigil ('#', '!') }, sites = { { host, api, secret, aliases } } its public servers (aliases: other hostnames its remotes use for one, its ssh hosts: 'ssh.dev.azure.com'), path = a regular expression over a git remote's path whose first group is the repo's name, takes = one over a pull request URL's path, shape = one over a whole remote URL that says a server looks like this host's (Azure DevOps' '/_git/'), which Oriel offers to set up as one, setup = { api = 'https://{host}/api/v4', secret = 'GITLAB_TOKEN', token_url, token_help, probe = fn(host, done) answering true for a server of this host's, from a request that sends no token } for a server of your own, and the functions, each answering with done(value) or done(nil, err), now or later. A site is { host, api, secret }: a server you set up has its own secret, 'GITLAB_TOKEN:git.acme.corp', and the plugin is let reach it. Reviews, per site: requested = fn(site, done) with the open pull requests asking you, and those you're part way through; lookup = fn(site, url, done) with one in full; checkout = fn(site, url, done) with { remote, ref }: a substring of the registered repo's remote, and the refspec a review session fetches; submit = fn(site, url, review, done) posts the user's review from a review session's pull request tab, review = { event ('approve', 'changes', 'comment'), body, commit = the head it read, comments = { { path, line, start_line, old = on the removed code, body } } }; react = fn(site, url, emoji, done) reacts to the pull request as you when a review session starts on it, with review.start_reaction ('👀'); unreact = fn(site, url, emoji, done) takes that reaction back once you've sent your review (not having it is no error). Ship, each given repo = { session, project, remote, repo, site }: for_branch = fn(repo, branch, done) with the branch's pull request, open first, or nil; create = fn(repo, { title, body, branch, base, draft }, done); edit = fn(repo, pr, title, body, done); ready = fn(repo, pr, done); request_reviewers = fn(repo, pr, { logins }, done); checks = fn(repo, pr, done) with { { name, state ('ok', 'failed', 'running', 'queued', 'skipped'), url, started, completed, detail = a failing one's log line } }; merge_methods = fn(repo, pr, done) with { allowed = { 'merge', 'squash', 'rebase' }, default }; blockers = fn(repo, pr, done) with { { kind, count, text, hard = the host would refuse, url } }; merge = fn(repo, pr, { method, head, title, body, delete_branch }, done). Review mirroring: threads = fn(repo, pr, done) with { { id ('' or nil for the conversation), path, line, old, outdated, resolved, reviewer, state ('changes', 'approved', 'commented'), url, comments = { { id, author, body, at, member = the author belongs to the repo, so the agent may read it } } } }; reply = fn(repo, pr, thread = { id, path, line, old, url }, body, done) with the new comment's id, posting the draft the user forwards (thread.id empty: the conversation); resolve = fn(repo, pr, thread, resolved, done) resolves a review thread there, or reopens it, when the user does in Oriel. Every answer crosses as JSON: an empty table is an empty list or object. A pull request is { number, id, title, body, url, author, state ('open', 'closed'), draft, merged, branch, base, head = its commit, reviewers }. An err that says HTTP 401 ('GitLab: the token was refused (HTTP 401)') is the host refusing the site's token, which Oriel stops sending. A missing function's part isn't offered. A request is { url, repo, number, title, author, draft, asked (RFC 3339, or seconds since 1970), via (the team asked, '' for you), state ('new', 're-requested', 'changes', 'commented', 'approved'), since (commits since your review), adds, dels, files, ci ('ok', 'failed', 'running'), base, head (its branch), opened, updated (times as asked's), reviewers = { { login ('@org/team' for a team), state ('requested', 'approved', 'changes', 'commented') } } who's asked and who has reviewed, shown when its row is hovered }. A detail is { request, base, head, commit = the head's, state ('open', 'closed', 'merged'), mergeable, labels = { names }, reviewers = { { login, state } }, body, files = { { path, adds, dels } }, checks, clone = the repo's clone URL, for Add repo…, events = { { kind ('push', 'review', 'comment'), who, at (RFC 3339), text, state = a review's ('approved', 'changes', 'commented', 'dismissed'), ref = a push's commit or a line comment's 'path:line', url, reactions } } oldest first, reactions = { { emoji ('👍'), count } } the PR's own; a host without reactions leaves them out }."
func (r CodeAPI) Host(spec script.Table, where string) error {
	h, err := codeHostSpec(r.a, spec, where)
	if err != nil {
		return err
	}
	codeHostFuncs(r.a, spec, &h)
	r.a.own(r.a.Host.Contribute(h))
	return nil
}

// codeHostSpec is a Lua code host's fields: its words, patterns, sites, tokens and setup, with the grants they need
// checked.
func codeHostSpec(a *APIs, spec script.Table, where string) (registry.CodeHost, error) {
	name := str(spec, "name")
	if name == "" {
		return registry.CodeHost{}, errors.New("a code host needs a name")
	}
	h := registry.CodeHost{Name: name, Title: str(spec, "title"), Icon: str(spec, "icon"), Order: 50, Source: where}
	if o, ok := spec["order"].(float64); ok {
		h.Order = int(o)
	}
	if w, ok := spec["words"].(script.Table); ok {
		h.Words = registry.Words{Short: str(w, "short"), Long: str(w, "long"), Sigil: str(w, "sigil")}
	}
	for k, re := range map[string]**regexp.Regexp{"takes": &h.Takes, "path": &h.Path, "shape": &h.Shape} {
		if v := str(spec, k); v != "" {
			c, err := regexp.Compile(v)
			if err != nil {
				return registry.CodeHost{}, fmt.Errorf("the code host's %s: %w", k, err)
			}
			*re = c
		}
	}
	if sites, ok := spec["sites"].([]any); ok {
		for _, x := range sites {
			if t, ok := x.(script.Table); ok {
				h.Sites = append(h.Sites, registry.Site{Host: str(t, "host"), API: str(t, "api"), Secret: str(t, "secret"), Aliases: plugin.Strings(t["aliases"])})
			}
		}
	}
	for _, s := range h.Sites {
		if s.Secret == "" {
			continue
		}
		if err := a.Plugin.Check(plugin.CapSecrets, s.Secret); err != nil {
			return registry.CodeHost{}, err
		}
	}
	if t, ok := spec["tokens"].(script.Table); ok {
		h.Tokens = registry.TokenSources{Env: plugin.Strings(t["env"]), HostedEnv: plugin.Strings(t["hosted_env"]), Command: plugin.Strings(t["command"])}
		h.Tokens.Optional, _ = t["optional"].(bool)
		// An env var is read under a secret the plugin declares by its name; a command needs its exec grant.
		for _, e := range append(slices.Clone(h.Tokens.Env), h.Tokens.HostedEnv...) {
			if err := a.Plugin.Check(plugin.CapSecrets, e); err != nil {
				return registry.CodeHost{}, fmt.Errorf("the code host's token from $%s: %w", e, err)
			}
		}
		if len(h.Tokens.Command) > 0 {
			prog, err := a.Plugin.CheckProgram(h.Tokens.Command[0], "")
			if err != nil {
				return registry.CodeHost{}, fmt.Errorf("the code host's token command: %w", err)
			}
			h.Tokens.Command[0] = prog
		}
	}
	if t, ok := spec["setup"].(script.Table); ok {
		h.Setup = registry.SiteSetup{API: str(t, "api"), Secret: str(t, "secret"), TokenURL: str(t, "token_url"), TokenHelp: str(t, "token_help")}
		if f, _ := t["probe"].(*script.Fn); f != nil {
			h.Setup.Probe = func(ctx context.Context, host string) (bool, error) {
				v, err := a.await(ctx, f, host)
				yes, _ := v.(bool)
				return yes, err
			}
		}
	}
	if g := a.Plugin.Guard; g != nil {
		id := a.Plugin.ID
		h.Allow = func(host, secret, why string) (undo func()) {
			gs := []plugin.Grant{{Cap: plugin.CapNet, Scope: []string{host}, Why: why}}
			if secret != "" {
				gs = append(gs, plugin.Grant{Cap: plugin.CapSecrets, Scope: []string{secret + "@" + host}, Why: why})
			}
			return g.Widen(id, why, gs...)
		}
	}
	return h, nil
}

// codeHostFuncs are a Lua code host's functions as h's: each answer is decoded from its JSON.
func codeHostFuncs(a *APIs, spec script.Table, h *registry.CodeHost) {
	fn := func(k string) *script.Fn { f, _ := spec[k].(*script.Fn); return f }
	if f := fn("requested"); f != nil {
		h.Requested = func(ctx context.Context, s registry.Site) ([]registry.ReviewRequest, error) {
			var rows []any
			if err := a.callAs(ctx, f, &rows, s); err != nil {
				return nil, err
			}
			out := []registry.ReviewRequest{}
			for _, row := range rows {
				var r registry.ReviewRequest
				if decodeTo(withTimes(row), &r) == nil {
					out = append(out, r)
				}
			}
			return out, nil
		}
	}
	if f := fn("lookup"); f != nil {
		h.Lookup = func(ctx context.Context, s registry.Site, url string) (registry.ReviewDetail, error) {
			var raw any
			if err := a.callAs(ctx, f, &raw, s, url); err != nil {
				return registry.ReviewDetail{}, err
			}
			var d registry.ReviewDetail
			if raw == nil || decodeTo(withTimes(raw), &d) != nil {
				return d, errors.New("no pull request " + url)
			}
			return d, nil
		}
	}
	if f := fn("checkout"); f != nil {
		h.Checkout = func(ctx context.Context, s registry.Site, url string) (registry.PRCheckout, error) {
			var c registry.PRCheckout
			if err := a.callAs(ctx, f, &c, s, url); err != nil {
				return c, err
			}
			if c.Remote == "" || c.Ref == "" {
				return c, errors.New("no checkout for " + url)
			}
			return c, nil
		}
	}
	if f := fn("submit"); f != nil {
		h.Submit = func(ctx context.Context, s registry.Site, url string, r registry.ReviewSubmit) error {
			return a.callAs(ctx, f, nil, s, url, r)
		}
	}
	if f := fn("react"); f != nil {
		h.React = func(ctx context.Context, s registry.Site, url, emoji string) error {
			return a.callAs(ctx, f, nil, s, url, emoji)
		}
	}
	if f := fn("unreact"); f != nil {
		h.Unreact = func(ctx context.Context, s registry.Site, url, emoji string) error {
			return a.callAs(ctx, f, nil, s, url, emoji)
		}
	}
	if f := fn("for_branch"); f != nil {
		h.ForBranch = func(ctx context.Context, r registry.CodeRepo, branch string) (*registry.PR, error) {
			var pr *registry.PR
			return pr, a.callAs(ctx, f, &pr, r, branch)
		}
	}
	if f := fn("create"); f != nil {
		h.Create = func(ctx context.Context, r registry.CodeRepo, n registry.NewPR) (*registry.PR, error) {
			var pr *registry.PR
			return pr, a.callAs(ctx, f, &pr, r, n)
		}
	}
	if f := fn("edit"); f != nil {
		h.Edit = func(ctx context.Context, r registry.CodeRepo, pr registry.PR, title, body string) (*registry.PR, error) {
			var out *registry.PR
			return out, a.callAs(ctx, f, &out, r, pr, title, body)
		}
	}
	if f := fn("ready"); f != nil {
		h.Ready = func(ctx context.Context, r registry.CodeRepo, pr registry.PR) error {
			return a.callAs(ctx, f, nil, r, pr)
		}
	}
	if f := fn("request_reviewers"); f != nil {
		h.RequestReviewers = func(ctx context.Context, r registry.CodeRepo, pr registry.PR, who []string) error {
			return a.callAs(ctx, f, nil, r, pr, who)
		}
	}
	if f := fn("checks"); f != nil {
		h.Checks = func(ctx context.Context, r registry.CodeRepo, pr registry.PR) ([]registry.Check, error) {
			var out []registry.Check
			return out, a.callAs(ctx, f, &out, r, pr)
		}
	}
	if f := fn("threads"); f != nil {
		h.Threads = func(ctx context.Context, r registry.CodeRepo, pr registry.PR) ([]registry.ReviewThread, error) {
			var out []registry.ReviewThread
			return out, a.callAs(ctx, f, &out, r, pr)
		}
	}
	if f := fn("reply"); f != nil {
		h.Reply = func(ctx context.Context, r registry.CodeRepo, pr registry.PR, thread registry.ReviewThread, body string) (string, error) {
			var id any
			if err := a.callAs(ctx, f, &id, r, pr, thread, body); err != nil || id == nil {
				return "", err
			}
			return fmt.Sprint(id), nil
		}
	}
	if f := fn("resolve"); f != nil {
		h.Resolve = func(ctx context.Context, r registry.CodeRepo, pr registry.PR, thread registry.ReviewThread, resolved bool) error {
			return a.callAs(ctx, f, nil, r, pr, thread, resolved)
		}
	}
	if f := fn("merge_methods"); f != nil {
		h.MergeMethods = func(ctx context.Context, r registry.CodeRepo, pr registry.PR) (registry.MergeMethods, error) {
			var out registry.MergeMethods
			return out, a.callAs(ctx, f, &out, r, pr)
		}
	}
	if f := fn("blockers"); f != nil {
		h.Blockers = func(ctx context.Context, r registry.CodeRepo, pr registry.PR) ([]registry.MergeBlocker, error) {
			var out []registry.MergeBlocker
			return out, a.callAs(ctx, f, &out, r, pr)
		}
	}
	if f := fn("merge"); f != nil {
		h.Merge = func(ctx context.Context, r registry.CodeRepo, pr registry.PR, o registry.MergeOpts) error {
			return a.callAs(ctx, f, nil, r, pr, o)
		}
	}
}

// callAs calls a plugin's f with args as Lua takes them, and decodes its answer into out: left as it is when f
// answers nothing, or out is nil.
func (a *APIs) callAs(ctx context.Context, f *script.Fn, out any, args ...any) error {
	for i, x := range args {
		args[i] = plainOf(x)
	}
	v, err := a.await(ctx, f, args...)
	if err != nil || out == nil || v == nil {
		return err
	}
	return decodeTo(v, out)
}

// plainOf is v as Lua takes it: its JSON, decoded.
func plainOf(v any) any {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	json.Unmarshal(b, &out)
	return out
}

// decodeTo reads what Lua answered into out, through its JSON; an empty table, wherever it is, is an empty list or
// object (null), as Lua can't tell them apart.
func decodeTo(v, out any) error {
	b, err := json.Marshal(emptyNil(v))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// withTimes is a plugin's review request, or a detail with one under request, with each time it gave as seconds since
// 1970 written as RFC 3339, as the request's JSON reads it.
func withTimes(v any) any {
	t, ok := v.(script.Table)
	if !ok {
		return v
	}
	out := script.Table{}
	for k, x := range t {
		out[k] = x
	}
	for _, k := range []string{"asked", "opened", "updated"} {
		if secs, ok := out[k].(float64); ok {
			out[k] = time.Unix(int64(secs), 0).UTC().Format(time.RFC3339)
		}
	}
	if rq, ok := out["request"]; ok {
		out["request"] = withTimes(rq)
	}
	return out
}

// emptyNil is v with each empty table in it nil.
func emptyNil(v any) any {
	switch v := v.(type) {
	case script.Table:
		if len(v) == 0 {
			return nil
		}
		out := script.Table{}
		for k, x := range v {
			out[k] = emptyNil(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = emptyNil(x)
		}
		return out
	}
	return v
}
