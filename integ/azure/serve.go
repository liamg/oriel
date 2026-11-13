package azure

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/registry"
)

// CodeNoKey is the JSON-RPC error of a request made before the site's token is set.
const CodeNoKey = registry.CodeSecretUnset

// CodeRepo is registry.CodeRepo as the Lua sends it, with its site.
type CodeRepo struct {
	Remote string `json:"remote"`
	Repo   string `json:"repo"`
	Site   Site   `json:"site"`
}

// Params are what the plugin's Lua sends: a code host function's arguments (registry.CodeHost), with the site the
// review functions are asked about.
type Params struct {
	Site   Site               `json:"site"`
	Host   string             `json:"host"` // probe's
	URL    string             `json:"url"`
	Repo   CodeRepo           `json:"repo"`
	Branch string             `json:"branch"`
	PR     *registry.PR       `json:"pr"`
	New    registry.NewPR     `json:"new"`
	Title  string             `json:"title"`
	Body   string             `json:"body"`
	Who    []string           `json:"who"`
	Thread string             `json:"thread_id"` // reply's: a thread's key, "" for a new one in the conversation
	Merge  registry.MergeOpts `json:"merge"`
	Orgs   []string           `json:"orgs"` // the site's organizations the Lua has learned, for requested
}

// Server answers the plugin's Lua. It learns the organizations on dev.azure.com to ask for reviews in from every
// repo and pull request it's asked about.
type Server struct {
	Client Client
	// Probe is one unauthenticated GET, answering its status and headers by lower-case name.
	Probe func(ctx context.Context, url string) (int, map[string]string, error)

	mu   sync.Mutex
	orgs map[string][]string // by site API
}

// Requested is requested's answer: the requests, and the site's organizations known, for the Lua to keep.
type Requested struct {
	Requests []registry.ReviewRequest `json:"requests"`
	Orgs     []string                 `json:"orgs"`
}

func (s *Server) learn(site Site, orgs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.orgs == nil {
		s.orgs = map[string][]string{}
	}
	for _, o := range orgs {
		if o != "" && !slices.Contains(s.orgs[site.API], o) {
			s.orgs[site.API] = append(s.orgs[site.API], o)
		}
	}
	slices.Sort(s.orgs[site.API])
}

func (s *Server) known(site Site) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.orgs[site.API])
}

// repo is the request's repository, from its remote on its site.
func (s *Server) repo(p Params) (Repo, error) {
	if p.Repo.Site.API == "" {
		return Repo{}, errors.New("no Azure DevOps site given")
	}
	r, ok := ParseRemote(p.Repo.Site, p.Repo.Remote)
	if !ok {
		return Repo{}, fmt.Errorf("%q isn't a repo on %s", p.Repo.Remote, p.Repo.Site.Host)
	}
	s.learn(r.Site, r.Org)
	return r, nil
}

// pr is the request's pull request's number.
func (p Params) pr() (int, error) {
	if p.PR == nil || p.PR.Number == 0 {
		return 0, errNoPR
	}
	return p.PR.Number, nil
}

// Handle answers one request, each in Oriel's shapes for a code host (registry.CodeHost).
func (s *Server) Handle(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	var p Params
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
	}
	var out any
	var err error
	switch method {
	case "probe":
		out, err = s.probe(ctx, p.Host)
	case "requested":
		out, err = s.requested(ctx, p)
	case "lookup":
		out, err = s.lookup(ctx, p.Site, p.URL)
	case "checkout":
		out, err = s.checkout(ctx, p.Site, p.URL)
	case "for_branch", "create", "edit", "ready", "request_reviewers", "checks", "merge_methods", "blockers", "merge", "threads", "reply":
		var r Repo
		if r, err = s.repo(p); err == nil {
			out, err = s.ship(ctx, method, r, p)
		}
	default:
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
	}
	if err != nil {
		return nil, noKey(err)
	}
	return out, nil
}

// noKey is err, said plainly when it's the host saying the site's token isn't set.
func noKey(err error) error {
	if registry.SecretUnset(err) {
		return &jsonrpc.Error{Code: CodeNoKey, Message: "no Azure DevOps token yet"}
	}
	return err
}

func (s *Server) ship(ctx context.Context, method string, r Repo, p Params) (any, error) {
	c := s.Client
	if method == "for_branch" {
		pr, err := c.ForBranch(ctx, r, p.Branch)
		if err != nil || pr == nil {
			return nil, err
		}
		return prOf(r, *pr), nil
	}
	if method == "create" {
		pr, err := c.Create(ctx, r, p.New.Title, p.New.Body, p.New.Branch, p.New.Base, p.New.Draft)
		if err != nil {
			return nil, err
		}
		return prOf(r, pr), nil
	}
	n, err := p.pr()
	if err != nil {
		return nil, err
	}
	switch method {
	case "edit":
		pr, err := c.Update(ctx, r, n, map[string]any{"title": p.Title, "description": p.Body})
		if err != nil {
			return nil, err
		}
		return prOf(r, pr), nil
	case "ready":
		_, err := c.Update(ctx, r, n, map[string]any{"isDraft": false})
		return map[string]any{}, err
	case "request_reviewers":
		return map[string]any{}, s.reviewers(ctx, r, n, p.Who)
	case "checks":
		st, err := c.Statuses(ctx, r, n)
		return checksOf(st), err
	case "merge_methods":
		return s.mergeMethods(ctx, r, *p.PR)
	case "blockers":
		return s.blockers(ctx, r, n)
	case "merge":
		return map[string]any{}, s.merge(ctx, r, *p.PR, p.Merge)
	case "threads":
		return s.threads(ctx, r, n)
	case "reply":
		return s.reply(ctx, r, n, p.Thread, p.Body)
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
}

var guid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// reviewers asks each to review: an id as it is, a unique name (an email, a team's "[project]\name") looked up.
func (s *Server) reviewers(ctx context.Context, r Repo, n int, who []string) error {
	for _, w := range who {
		w = strings.TrimPrefix(strings.TrimSpace(w), "@")
		if w == "" {
			continue
		}
		id := w
		if !guid.MatchString(w) {
			ident, err := s.Client.Lookup(ctx, r, w)
			if err != nil {
				return err
			}
			id = ident.ID
		}
		if err := s.Client.AddReviewer(ctx, r, n, id); err != nil {
			return err
		}
	}
	return nil
}

// strategies are Oriel's merge methods as Azure DevOps names them.
var strategies = map[string]string{registry.MergeCommit: "noFastForward", registry.MergeSquash: "squash", registry.MergeRebase: "rebase"}

func (s *Server) mergeMethods(ctx context.Context, r Repo, pr registry.PR) (registry.MergeMethods, error) {
	types, err := s.Client.MergeTypes(ctx, r, pr.ID, pr.Base)
	if err != nil {
		return registry.MergeMethods{}, err
	}
	if len(types) == 0 {
		return registry.MergeMethods{Allowed: []string{registry.MergeCommit, registry.MergeSquash, registry.MergeRebase}}, nil
	}
	m := registry.MergeMethods{Allowed: types}
	if len(types) == 1 {
		m.Default = types[0]
	}
	return m, nil
}

// blockers are what Azure DevOps says of completing now: active threads, votes against, required reviewers not yet
// approving, a draft and a conflict, which last three it refuses outright.
func (s *Server) blockers(ctx context.Context, r Repo, n int) ([]registry.MergeBlocker, error) {
	c := s.Client
	pr, err := c.PR(ctx, r, n)
	if err != nil {
		return nil, err
	}
	threads, err := c.Threads(ctx, r, n)
	if err != nil {
		return nil, err
	}
	web := r.Web() + "/pullrequest/" + strconv.Itoa(n)
	out := []registry.MergeBlocker{}
	open := 0
	for _, t := range threads {
		if t.Open() {
			open++
		}
	}
	if open > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockThreads, Count: open, Text: counted(open, "reviewer comment") + " unresolved", URL: web})
	}
	var against, waiting []string
	for _, rv := range pr.Reviewers {
		switch {
		case rv.Vote < 0:
			against = append(against, rv.DisplayName)
		case rv.IsRequired && rv.Vote < 5:
			waiting = append(waiting, rv.DisplayName)
		}
	}
	if len(against) > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockChanges, Count: len(against), Text: strings.Join(against, ", ") + " asked for changes", URL: web})
	}
	if len(waiting) > 0 {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockReviews, Count: len(waiting),
			Text: strings.Join(waiting, ", ") + " must approve first", Hard: true, URL: web})
	}
	if pr.IsDraft {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockDraft, Text: "it's a draft: publish it first", Hard: true})
	}
	if pr.MergeStatus == "conflicts" {
		out = append(out, registry.MergeBlocker{Kind: registry.BlockConflicts, Text: "it conflicts with " + strings.TrimPrefix(pr.Target, "refs/heads/"), Hard: true})
	}
	return out, nil
}

// merge completes the pull request at the commit the user saw.
func (s *Server) merge(ctx context.Context, r Repo, pr registry.PR, o registry.MergeOpts) error {
	strategy := strategies[o.Method]
	if strategy == "" {
		return fmt.Errorf("Azure DevOps doesn't merge by %q", o.Method)
	}
	opts := map[string]any{"mergeStrategy": strategy, "deleteSourceBranch": o.DeleteBranch}
	if msg := strings.TrimSpace(o.Title + "\n\n" + o.Body); msg != "" {
		opts["mergeCommitMessage"] = msg
	}
	in := map[string]any{"status": "completed", "lastMergeSourceCommit": map[string]any{"commitId": cmp.Or(o.Head, pr.Head)}, "completionOptions": opts}
	_, err := s.Client.Update(ctx, r, pr.Number, in)
	return err
}

// threads are a pull request's review mirrored (registry.ReviewThread): each thread on a line of a file, by its
// id, then one with no id holding the conversation's comments. A comment's key is "thread/comment", as Azure DevOps
// numbers comments within their thread.
func (s *Server) threads(ctx context.Context, r Repo, n int) ([]registry.ReviewThread, error) {
	c := s.Client
	pr, err := c.PR(ctx, r, n)
	if err != nil {
		return nil, err
	}
	ts, err := c.Threads(ctx, r, n)
	if err != nil {
		return nil, err
	}
	// A member is the PR's author or a reviewer on it: asking Azure DevOps who's in the project would take a call
	// per author and a scope the token may lack.
	votes, members := map[string]int{}, map[string]bool{pr.CreatedBy.ID: true}
	for _, rv := range pr.Reviewers {
		votes[rv.ID], members[rv.ID] = rv.Vote, true
	}
	web := r.Web() + "/pullrequest/" + strconv.Itoa(n)
	out := []registry.ReviewThread{}
	general := registry.ReviewThread{URL: web, Comments: []registry.ReviewNote{}}
	for _, t := range ts {
		if t.IsDeleted || t.System() {
			continue
		}
		var notes []registry.ReviewNote
		for _, cm := range t.Comments {
			if cm.IsDeleted || cm.CommentType == "system" {
				continue
			}
			at, _ := time.Parse(time.RFC3339, cm.Published)
			notes = append(notes, registry.ReviewNote{ID: strconv.Itoa(t.ID) + "/" + strconv.Itoa(cm.ID),
				Author: cmp.Or(cm.Author.UniqueName, cm.Author.DisplayName), Body: cm.Content, At: at, Member: members[cm.Author.ID]})
		}
		if len(notes) == 0 {
			continue
		}
		if t.Context == nil || t.Context.FilePath == "" {
			general.Comments = append(general.Comments, notes...)
			continue
		}
		resolved := t.Resolved()
		rt := registry.ReviewThread{ID: strconv.Itoa(t.ID), Path: strings.TrimPrefix(t.Context.FilePath, "/"), Resolved: &resolved,
			URL: web + "?discussionId=" + strconv.Itoa(t.ID), Comments: notes}
		switch {
		case t.Context.Right != nil:
			rt.Line = t.Context.Right.Line
		case t.Context.Left != nil:
			rt.Line, rt.Old = t.Context.Left.Line, true
		}
		first := t.Comments[0].Author
		rt.Reviewer, rt.State = cmp.Or(first.UniqueName, first.DisplayName), stateOf(votes[first.ID])
		out = append(out, rt)
	}
	if len(general.Comments) > 0 {
		out = append(out, general)
	}
	return out, nil
}

// stateOf is a reviewer's vote as their review's state.
func stateOf(vote int) string {
	switch {
	case vote >= 5:
		return registry.ReviewApproved
	case vote < 0:
		return registry.ReviewChanges
	}
	return registry.ReviewCommented
}

// reply posts body in a thread (its key), or as a new thread in the conversation, and answers the comment's key.
func (s *Server) reply(ctx context.Context, r Repo, n int, thread, body string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", errors.New("the reply is empty")
	}
	if thread == "" {
		t, err := s.Client.NewThread(ctx, r, n, body)
		if err != nil {
			return "", err
		}
		if len(t.Comments) == 0 {
			return strconv.Itoa(t.ID) + "/1", nil
		}
		return strconv.Itoa(t.ID) + "/" + strconv.Itoa(t.Comments[0].ID), nil
	}
	tid, err := strconv.Atoi(thread)
	if err != nil {
		return "", fmt.Errorf("not an Azure DevOps thread: %q", thread)
	}
	cm, err := s.Client.Reply(ctx, r, n, tid, body)
	if err != nil {
		return "", err
	}
	return thread + "/" + strconv.Itoa(cm.ID), nil
}

// requested are the active pull requests asking you on a site, and those you've voted on: in each organization known
// on dev.azure.com, in its collection on a server.
func (s *Server) requested(ctx context.Context, p Params) (Requested, error) {
	if p.Site.API == "" {
		return Requested{}, errors.New("no Azure DevOps site given")
	}
	s.learn(p.Site, p.Orgs...)
	out := Requested{Requests: []registry.ReviewRequest{}, Orgs: []string{}}
	accounts := []Account{{Site: p.Site}}
	if p.Site.orgs() {
		accounts = nil
		for _, org := range s.known(p.Site) {
			accounts = append(accounts, Account{Site: p.Site, Org: org})
		}
	}
	var errs []error
	for _, a := range accounts {
		me, err := s.Client.Me(ctx, a)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		prs, err := s.Client.Reviewing(ctx, a, me.ID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, pr := range prs {
			out.Requests = append(out.Requests, requestOf(a, me, pr))
		}
	}
	out.Orgs = append(out.Orgs, s.known(p.Site)...)
	if len(out.Requests) == 0 && len(errs) > 0 {
		return out, errs[0]
	}
	return out, nil
}

// requestOf is a pull request asking me, as a row of the Reviews page: my vote as its state, or the team asked.
func requestOf(a Account, me Identity, pr PullRequest) registry.ReviewRequest {
	r := Repo{Site: a.Site, Org: a.Org, Project: pr.Repository.Project.Name, Name: pr.Repository.Name}
	q := registry.ReviewRequest{URL: r.Web() + "/pullrequest/" + strconv.Itoa(pr.ID), Repo: r.Project + "/" + r.Name, Number: pr.ID, Title: pr.Title,
		Author: pr.CreatedBy.DisplayName, Draft: pr.IsDraft, State: registry.ReviewNew}
	q.Asked, _ = time.Parse(time.RFC3339, pr.Created)
	direct := false
	for _, rv := range pr.Reviewers {
		if rv.ID != me.ID {
			continue
		}
		direct = true
		switch {
		case rv.Vote >= 5:
			q.State = registry.ReviewApproved
		case rv.Vote < 0:
			q.State = registry.ReviewChanges
		}
	}
	if !direct {
		for _, rv := range pr.Reviewers {
			if rv.IsContainer {
				q.Via = "@" + rv.DisplayName
				break
			}
		}
	}
	return q
}

func (s *Server) lookup(ctx context.Context, site Site, raw string) (registry.ReviewDetail, error) {
	r, n, err := ParsePR(site, raw)
	if err != nil {
		return registry.ReviewDetail{}, err
	}
	s.learn(site, r.Org)
	c := s.Client
	pr, err := c.PR(ctx, r, n)
	if err != nil {
		return registry.ReviewDetail{}, err
	}
	d := registry.ReviewDetail{Request: registry.ReviewRequest{URL: r.Web() + "/pullrequest/" + strconv.Itoa(n), Repo: r.Project + "/" + r.Name,
		Number: n, Title: pr.Title, Author: pr.CreatedBy.DisplayName, Draft: pr.IsDraft},
		Base: strings.TrimPrefix(pr.Target, "refs/heads/"), Head: strings.TrimPrefix(pr.Source, "refs/heads/"), Body: pr.Description,
		Clone: cmp.Or(pr.Repository.RemoteURL, r.Web())}
	switch pr.MergeStatus {
	case "succeeded":
		d.Mergeable = "yes"
	case "conflicts":
		d.Mergeable = "conflicts"
	}
	for _, rv := range pr.Reviewers {
		state := "requested"
		switch {
		case rv.Vote >= 5:
			state = "approved"
		case rv.Vote < 0:
			state = "changes"
		}
		d.Reviewers = append(d.Reviewers, registry.PRReviewer{Login: cmp.Or(rv.UniqueName, rv.DisplayName), State: state})
	}
	changes, err := c.Changes(ctx, r, n)
	if err != nil {
		return d, err
	}
	for _, ch := range changes {
		d.Files = append(d.Files, registry.PRFileChange{Path: ch.Path})
	}
	d.Request.Files = len(d.Files)
	st, err := c.Statuses(ctx, r, n)
	if err != nil {
		return d, err
	}
	d.Checks = checksOf(st)
	return d, nil
}

// checkout is where a review session fetches a pull request from: the repo's https remote less its scheme, and the
// source branch.
func (s *Server) checkout(ctx context.Context, site Site, raw string) (registry.PRCheckout, error) {
	r, n, err := ParsePR(site, raw)
	if err != nil {
		return registry.PRCheckout{}, err
	}
	pr, err := s.Client.PR(ctx, r, n)
	if err != nil {
		return registry.PRCheckout{}, err
	}
	return registry.PRCheckout{Remote: strings.TrimPrefix(r.Web(), "https://"), Ref: pr.Source}, nil
}

// probe says whether host is an Azure DevOps Server, from one unauthenticated request: its connectionData answers
// with Azure DevOps' own headers whether or not it lets us in.
func (s *Server) probe(ctx context.Context, host string) (map[string]bool, error) {
	if host == "" || strings.ContainsAny(host, "/?#@") {
		return nil, fmt.Errorf("not a host: %q", host)
	}
	_, headers, err := s.Probe(ctx, "https://"+host+"/_apis/connectionData")
	if err != nil {
		return map[string]bool{"server": false}, nil
	}
	for k := range headers {
		if k := strings.ToLower(k); k == "x-tfs-processid" || strings.HasPrefix(k, "x-vss-") {
			return map[string]bool{"server": true}, nil
		}
	}
	return map[string]bool{"server": false}, nil
}

// prOf is a pull request as a code host tells it.
func prOf(r Repo, p PullRequest) registry.PR {
	out := registry.PR{Number: p.ID, ID: p.Repository.ID, Title: p.Title, Body: p.Description, URL: r.Web() + "/pullrequest/" + strconv.Itoa(p.ID),
		Author: p.CreatedBy.DisplayName, State: "closed", Draft: p.IsDraft, Merged: p.Status == "completed",
		Branch: strings.TrimPrefix(p.Source, "refs/heads/"), Base: strings.TrimPrefix(p.Target, "refs/heads/"), Head: p.LastSource.CommitID}
	if p.Status == "active" {
		out.State = "open"
	}
	for _, rv := range p.Reviewers {
		out.Reviewers = append(out.Reviewers, cmp.Or(rv.UniqueName, rv.DisplayName))
	}
	return out
}

// checksOf are a pull request's statuses as checks: each context's latest.
func checksOf(st []Status) []registry.Check {
	latest := map[string]Status{}
	var order []string
	for _, s := range st {
		name := s.Context.Name
		if s.Context.Genre != "" {
			name = s.Context.Genre + "/" + name
		}
		if o, ok := latest[name]; !ok {
			order = append(order, name)
		} else if o.ID > s.ID {
			continue
		}
		latest[name] = s
	}
	out := []registry.Check{}
	for _, name := range order {
		s := latest[name]
		c := registry.Check{Name: name, URL: s.TargetURL}
		c.Started, _ = time.Parse(time.RFC3339, s.Created)
		done, _ := time.Parse(time.RFC3339, s.Updated)
		switch s.State {
		case "succeeded":
			c.State, c.Completed = registry.CheckOK, done
		case "failed", "error":
			c.State, c.Completed, c.Detail = registry.CheckFailed, done, s.Description
		case "pending":
			c.State = registry.CheckRunning
		case "notApplicable":
			c.State = registry.CheckSkipped
		default:
			c.State = registry.CheckQueued
		}
		out = append(out, c)
	}
	return out
}

func counted(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

// Serve answers the Lua over r and w (the process's stdin and stdout) until it closes. Its requests to Azure DevOps
// go back over the same connection, as http.request, for the host to make with the site's token.
func Serve(r io.Reader, w io.Writer) error {
	s := &Server{}
	var conn *jsonrpc.Conn
	s.Client = Client{Send: func(ctx context.Context, req Request) (int, []byte, error) {
		params := map[string]any{"method": req.Method, "url": req.URL, "auth": req.Secret, "auth_scheme": "basic",
			"headers": map[string]string{"Accept": "application/json"}}
		if req.Body != nil {
			b, err := json.Marshal(req.Body)
			if err != nil {
				return 0, nil, err
			}
			params["body"] = string(b)
			params["headers"] = map[string]string{"Accept": "application/json", "Content-Type": "application/json"}
		}
		var res struct {
			Status int    `json:"status"`
			Body   string `json:"body"`
		}
		err := conn.Call(ctx, "http.request", params, &res)
		return res.Status, []byte(res.Body), err
	}}
	s.Probe = func(ctx context.Context, url string) (int, map[string]string, error) {
		var res struct {
			Status  int               `json:"status"`
			Headers map[string]string `json:"headers"`
		}
		err := conn.Call(ctx, "http.request", map[string]any{"method": "GET", "url": url, "timeout": 10}, &res)
		return res.Status, res.Headers, err
	}
	// The conn starts reading as it's made, so a request waits for conn to be set before it can call back.
	ready := make(chan struct{})
	conn = jsonrpc.NewConn(r, w, jsonrpc.Handlers{Request: func(req *jsonrpc.Request) {
		go func() {
			<-ready
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			req.Reply(s.Handle(ctx, req.Method, req.Params))
		}()
	}}, jsonrpc.Options{Version: "2.0"})
	close(ready)
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, jsonrpc.ErrClosed) {
		return err
	}
	return nil
}
