package ship_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// fakeGitea is the few Gitea API v1 endpoints the example plugin calls, for one user ("you") with token giteaToken.
type fakeGitea struct {
	*httptest.Server
	mu       sync.Mutex
	prs      map[string][]*giteaPR       // by owner/name
	comments map[string][]map[string]any // review comments, by owner/name#n
	issue    map[string][]map[string]any // the conversation, by owner/name#n
	merged   map[string]string           // the style each was merged with, by owner/name#n
	asks     []giteaAsk                  // pull requests asking you to review
}

const giteaToken = "gitea-tok"

type giteaPR struct {
	Number    int    `json:"number"`
	ID        int    `json:"id"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	HTMLURL   string `json:"html_url"`
	State     string `json:"state"`
	Merged    bool   `json:"merged"`
	Mergeable bool   `json:"mergeable"`
	User      struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Reviewers []map[string]string `json:"requested_reviewers"`
}

type giteaAsk struct {
	repo, title, author string
	number              int
}

func newFakeGitea(t *testing.T) *fakeGitea {
	isolate.Test(t)
	f := &fakeGitea{prs: map[string][]*giteaPR{}, comments: map[string][]map[string]any{}, issue: map[string][]map[string]any{}, merged: map[string]string{}}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	repo := func(r *http.Request) string { return r.PathValue("o") + "/" + r.PathValue("r") }
	find := func(r *http.Request) *giteaPR {
		n, _ := strconv.Atoi(r.PathValue("n"))
		for _, p := range f.prs[repo(r)] {
			if p.Number == n {
				return p
			}
		}
		return nil
	}
	mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, map[string]string{"version": "1.22.0"}) })
	mux.HandleFunc("GET /api/v1/repos/issues/search", func(w http.ResponseWriter, _ *http.Request) {
		out := []map[string]any{}
		for _, a := range f.asks {
			out = append(out, map[string]any{"number": a.number, "title": a.title, "html_url": "https://gitea.com/" + a.repo + "/pulls/" + strconv.Itoa(a.number),
				"user": map[string]string{"login": a.author}, "repository": map[string]string{"full_name": a.repo}, "created_at": "2026-10-19T09:00:00Z"})
		}
		reply(w, 200, out)
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}", func(w http.ResponseWriter, _ *http.Request) {
		reply(w, 200, map[string]any{"allow_merge_commits": true, "allow_squash_merge": true, "allow_rebase": true, "default_merge_style": "squash"})
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/pulls", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, append([]*giteaPR{}, f.prs[repo(r)]...))
	})
	mux.HandleFunc("POST /api/v1/repos/{o}/{r}/pulls", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Title, Body, Head, Base string }
		json.NewDecoder(r.Body).Decode(&in)
		n := 100 + len(f.prs[repo(r)])
		p := &giteaPR{Number: n, ID: 9000 + n, Title: in.Title, Body: in.Body, State: "open", Mergeable: true,
			HTMLURL: fmt.Sprintf("https://gitea.com/%s/pulls/%d", repo(r), n), Reviewers: []map[string]string{}}
		p.User.Login, p.Head.Ref, p.Head.SHA, p.Base.Ref = "you", in.Head, fmt.Sprintf("%040d", n), in.Base
		f.prs[repo(r)] = append(f.prs[repo(r)], p)
		reply(w, 201, p)
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/pulls/{n}", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, find(r)) })
	mux.HandleFunc("PATCH /api/v1/repos/{o}/{r}/pulls/{n}", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Title, Body *string }
		json.NewDecoder(r.Body).Decode(&in)
		p := find(r)
		if in.Title != nil {
			p.Title = *in.Title
		}
		if in.Body != nil {
			p.Body = *in.Body
		}
		reply(w, 200, p)
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/pulls/{n}/files", func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, []any{}) })
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/commits/{sha}/statuses", func(w http.ResponseWriter, _ *http.Request) { reply(w, 200, []any{}) })
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/pulls/{n}/reviews", func(w http.ResponseWriter, r *http.Request) {
		cs := f.comments[repo(r)+"#"+r.PathValue("n")]
		if len(cs) == 0 {
			reply(w, 200, []any{})
			return
		}
		reply(w, 200, []map[string]any{{"id": 1, "state": "COMMENT", "comments_count": len(cs), "user": map[string]string{"login": "priya"}}})
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/pulls/{n}/reviews/{id}/comments", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, append([]map[string]any{}, f.comments[repo(r)+"#"+r.PathValue("n")]...))
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/issues/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, append([]map[string]any{}, f.issue[repo(r)+"#"+r.PathValue("n")]...))
	})
	mux.HandleFunc("POST /api/v1/repos/{o}/{r}/issues/{n}/comments", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Body string }
		json.NewDecoder(r.Body).Decode(&in)
		k := repo(r) + "#" + r.PathValue("n")
		c := map[string]any{"id": 500 + len(f.issue[k]), "body": in.Body, "user": map[string]string{"login": "you"}, "created_at": "2026-10-19T10:00:00Z"}
		f.issue[k] = append(f.issue[k], c)
		reply(w, 201, c)
	})
	mux.HandleFunc("GET /api/v1/repos/{o}/{r}/collaborators/{user}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("user") == "priya" || r.PathValue("user") == "you" {
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
	})
	mux.HandleFunc("POST /api/v1/repos/{o}/{r}/pulls/{n}/merge", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Do   string `json:"Do"`
			Head string `json:"head_commit_id"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		p := find(r)
		if in.Head != p.Head.SHA {
			reply(w, 409, map[string]string{"message": "head out of date"})
			return
		}
		p.State, p.Merged = "closed", true
		f.merged[repo(r)+"#"+r.PathValue("n")] = in.Do
		w.WriteHeader(200)
	})
	f.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.URL.Path != "/api/v1/version" && r.Header.Get("Authorization") != "token "+giteaToken {
			reply(w, 401, map[string]string{"message": "token is required"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.Close)
	return f
}

// loadGitea runs docs/examples/gitea as an installed plugin, granted what its manifest declares.
func loadGitea(t *testing.T, s *apptest.Scene) {
	t.Helper()
	dir := hosttest.Install(t, "../../docs/examples/gitea")
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: dir, Guard: g})
	src, err := os.ReadFile(filepath.Join(dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
}

// giteaRepo is a fixture checkout whose origin is https://gitea.com/acme/<name>.git, answered by a bare repo on disk.
func giteaRepo(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	origins := filepath.Join(root, "origins")
	checkout := filepath.Join(root, name)
	git(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(origins, name+".git"))
	git(t, root, "init", "-q", "-b", "main", checkout)
	writeFiles(t, checkout, map[string]string{"main.go": "package main\n"})
	git(t, checkout, "add", "-A")
	git(t, checkout, "commit", "-q", "-m", "first")
	git(t, checkout, "remote", "add", "origin", "https://gitea.com/acme/"+name+".git")
	git(t, checkout, "config", "url."+origins+"/.insteadOf", "https://gitea.com/acme/")
	git(t, checkout, "push", "-q", "origin", "main")
	git(t, checkout, "fetch", "-q", "origin")
	git(t, checkout, "remote", "set-head", "origin", "main")
	return checkout
}

// A Gitea plugin can be plain Lua with no Go process, and docs/examples/gitea is that plugin: a work
// session on a repo on Gitea raises its PR from Ship, Merge… warns of an unresolved review comment and Continue merges
// it at the head shown, with the repo's default style; the pull requests asking you list on the Reviews page.
func TestGiteaExamplePlugin(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	gt := newFakeGitea(t)
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret("GITEA_TOKEN", giteaToken)
	hub := ghtest.New(t)
	lw := rig.NewHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	app := giteaRepo(t, "app")
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		apptest.Connect(lw.W)(cfg)
		cfg.Net, cfg.Secrets = hosttest.Net(gt.Server), secrets
	}})
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	loadGitea(t, s)
	s.Run("session.start", registry.Args{"kind": "work", "prompt": "Add a health check", "provider": "fake", "repos": []string{app}})
	st := rig.WaitState(t, s.Store(), "the session in front", func(st core.State) bool { return st.Focus != "" })
	sess, _ := st.Session(st.Focus)
	se := sess.Snapshot()
	s.Reopen()
	x := shipOf(s)

	writeFiles(t, shipDir(se, "app"), map[string]string{"health.go": "package main\n\nfunc health() {}\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "app"); return len(sh.Files) == 1 && sh.Code != nil })
	if sh, _ := x.ShipOf(se.ID, "app"); sh.Code.Name != "gitea" || sh.Repo != "acme/app" {
		t.Fatalf("the project's code host: %+v, repo %q", sh.Code, sh.Repo)
	}
	s.Run("ship.commit", registry.Args{"project": "app", "message": "feat: health check"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "app"); return sh.Commits == 1 })
	s.Run("ship.open", nil)
	s.Until("Raise PR…", func() bool { return strings.Contains(s.Text(), "Raise PR…") })

	s.Run("ship.raise", registry.Args{"project": "app", "title": "feat: health check"})
	shipSettle(s, t, "the PR", func() bool { sh, _ := x.ShipOf(se.ID, "app"); return sh.PR != nil })
	gt.mu.Lock()
	prs := gt.prs["acme/app"]
	gt.mu.Unlock()
	if len(prs) != 1 || prs[0].Title != "feat: health check" || prs[0].Head.Ref != se.Branch || prs[0].Base.Ref != "main" {
		t.Fatalf("the PR on Gitea: %+v", prs)
	}
	s.Until("the PR's number", func() bool { return strings.Contains(s.Text(), "#100 · feat: health check") })

	// Review mirroring: priya's comment on a line is a thread of the session's; the agent's reply is a draft until it's
	// forwarded, then a comment on the PR.
	ag := lw.Fake.WaitStarted(t)
	gt.mu.Lock()
	gt.comments["acme/app#100"] = []map[string]any{{"id": 7, "body": "Should this check the database too?", "path": "health.go", "position": 3,
		"resolver": nil, "user": map[string]string{"login": "priya"}, "created_at": "2026-10-19T09:30:00Z", "html_url": prs[0].HTMLURL + "/files#issuecomment-7"}}
	gt.mu.Unlock()
	s.Run("ship.refresh", nil)
	var tid core.ThreadID
	shipSettle(s, t, "the mirrored comment", func() bool {
		sess, _ := s.Store().State().Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Hosted != nil && th.Hosted.Host == "gitea" && th.Hosted.Key == "7" {
				tid = th.ID
			}
		}
		return tid != ""
	})
	ag.Emit(agent.TurnEnd{})
	shipSettle(s, t, "the comment handed to the agent", func() bool {
		sess, _ := s.Store().State().Session(se.ID)
		th, _ := sess.Snapshot().Thread(tid)
		return len(th.Undelivered()) == 0
	})
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "It does now: it pings the pool."}); !strings.Contains(out, "drafted") {
		t.Fatalf("reply: %q", out)
	}
	d := draftOf(t, s, se.ID, tid)
	s.Run("github.forward", registry.Args{"thread": string(tid)})
	shipSettle(s, t, "the forward", func() bool {
		sess, _ := s.Store().State().Session(se.ID)
		th, _ := sess.Snapshot().Thread(tid)
		for _, c := range th.Comments {
			if c.ID == d.ID && !c.Sent.IsZero() {
				return true
			}
		}
		return false
	})
	gt.mu.Lock()
	convo := gt.issue["acme/app#100"]
	gt.mu.Unlock()
	if len(convo) != 1 || convo[0]["body"] != "> On `health.go:3` (review comment 7)\n\nIt does now: it pings the pool." {
		t.Errorf("the conversation on Gitea: %v", convo)
	}

	s.Run("ship.merge", registry.Args{"project": "app"})
	shipSettle(s, t, "the warning", func() bool { return strings.Contains(s.Text(), "1 reviewer comment unresolved") })
	s.WantFrame("MERGE #100", "Continue", "Cancel")
	s.ClickText("Continue")
	shipSettle(s, t, "merged, and the next PR started", func() bool { p := pastPRs(s, se.ID); return len(p) == 1 && p[0].State == "merged" })
	gt.mu.Lock()
	how := gt.merged["acme/app#100"]
	gt.asks = append(gt.asks, giteaAsk{repo: "acme/api", number: 12, title: "Cache the token lookup", author: "priya"})
	gt.mu.Unlock()
	if how != "squash" {
		t.Errorf("merged with %q, want the repo's default, squash", how)
	}

	s.Run("reviews.open", nil)
	s.Until("the request", func() bool { return strings.Contains(s.Text(), "Cache the token lookup") })
	s.WantFrame("acme/api#12")
}
