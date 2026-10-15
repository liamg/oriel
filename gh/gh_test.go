package gh_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

// TestMain doubles as the gh command for the CLI transport's tests: run with GHTEST_BASE set, it answers
// `gh api --method M -H … path [--input -]` from the fake at that URL, the way gh reports its errors.
func TestMain(m *testing.M) {
	if base := os.Getenv("GHTEST_BASE"); base != "" {
		os.Exit(fakeGH(base, os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeGH(base string, args []string) int {
	method, path, input := "GET", "", false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--method":
			method = args[i+1]
			i++
		case "-H":
			i++
		case "--input":
			input = true
			i++
		default:
			path = args[i]
		}
	}
	var in any
	if input {
		b, _ := io.ReadAll(os.Stdin)
		json.Unmarshal(b, &in)
	}
	var out string
	if err := (gh.HTTP{Base: base}).Do(context.Background(), method, path, in, &out); err != nil {
		e := err.(*gh.Error)
		fmt.Fprintf(os.Stdout, `{"message":%q}`, e.Message)
		fmt.Fprintf(os.Stderr, "gh: %s (HTTP %d)\n", e.Message, e.Status)
		return 1
	}
	fmt.Print(out)
	return 0
}

// client is the API over a transport, http or cli, against the fake.
func client(t *testing.T, transport string, s *ghtest.Server) gh.Client {
	if transport == "http" {
		return s.Client()
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return gh.API{T: gh.CLI{Bin: exe, Dir: t.TempDir(), Env: []string{"GHTEST_BASE=" + s.URL}}}
}

var acme = gh.Repo{Owner: "acme", Name: "api"}

// FG-6 done when: "flows tested against fixture repos and a fake GitHub": the PR for a branch, opening one,
// editing it, its reviewers, out of draft, its checks and a failing one's log, over the REST API and through gh.
func TestPullRequestFlow(t *testing.T) {
	for _, transport := range []string{"http", "cli"} {
		t.Run(transport, func(t *testing.T) {
			s := ghtest.New(t)
			c := client(t, transport, s)
			ctx := context.Background()
			if p, err := c.PRForBranch(ctx, acme, "rate-limits"); err != nil || p != nil {
				t.Fatalf("before any PR: %+v, %v", p, err)
			}
			s.AddPR("acme/api", gh.PR{Title: "old", Head: "rate-limits", Base: "main", State: "closed"})
			p, err := c.CreatePR(ctx, acme, gh.NewPR{Title: "feat: limits", Body: "## What\n", Head: "rate-limits", Base: "main", Draft: true})
			if err != nil {
				t.Fatal(err)
			}
			if p.Number == 0 || !p.Draft || !p.Open() || p.URL == "" || p.Head != "rate-limits" {
				t.Fatalf("created %+v", p)
			}
			if _, err := c.CreatePR(ctx, acme, gh.NewPR{Title: "again", Head: "rate-limits", Base: "main"}); err == nil ||
				!strings.Contains(err.Error(), "already exists") {
				t.Errorf("a second PR for the branch: %v", err)
			}
			got, err := c.PRForBranch(ctx, acme, "rate-limits")
			if err != nil || got == nil || got.Number != p.Number {
				t.Fatalf("the branch's PR is %+v, %v; want the open #%d, not the closed one", got, err, p.Number)
			}
			if _, err := c.EditPR(ctx, acme, p.Number, "feat: rate limits", "## What\nmore\n"); err != nil {
				t.Fatal(err)
			}
			if err := c.RequestReviewers(ctx, acme, p.Number, []string{"@acme/platform", "jkowalski"}); err != nil {
				t.Fatal(err)
			}
			if err := c.MarkReady(ctx, acme, got); err != nil {
				t.Fatal(err)
			}
			got, _ = c.PRForBranch(ctx, acme, "rate-limits")
			if got.Title != "feat: rate limits" || got.Body != "## What\nmore\n" || got.Draft ||
				!slices.Equal(got.Reviewers, []string{"jkowalski", "acme/platform"}) {
				t.Fatalf("after editing: %+v", got)
			}

			t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
			s.SetChecks("acme/api", "rate-limits",
				gh.Check{ID: 1, Name: "lint", Status: "completed", Conclusion: "success", Actions: true, Started: t0, Completed: t0.Add(38 * time.Second)},
				gh.Check{ID: 2, Name: "unit", Status: "completed", Conclusion: "failure", Actions: true, Started: t0, Completed: t0.Add(time.Minute)},
				gh.Check{ID: 3, Name: "e2e", Status: "in_progress", Actions: true, Started: t0},
				gh.Check{Name: "preview deploy", Status: "queued"},
			)
			s.SetLog(2, "2026-10-14T09:00:01.0000000Z ##[group]Run go test ./...\n2026-10-14T09:00:02.1234567Z ok  \tlimiter/bucket\n"+
				"2026-10-14T09:00:03.0000000Z --- FAIL: TestBurst (0.02s)\n2026-10-14T09:00:04.0000000Z ##[error]Process completed with exit code 1.\n")
			cs, err := c.Checks(ctx, acme, "rate-limits")
			if err != nil {
				t.Fatal(err)
			}
			var states []string
			for _, ch := range cs {
				states = append(states, ch.Name+":"+ch.State())
			}
			if want := []string{"lint:pass", "unit:fail", "e2e:pending", "preview deploy:pending"}; !slices.Equal(states, want) {
				t.Fatalf("checks %q, want %q", states, want)
			}
			if d := cs[0].Took(t0); d != 38*time.Second {
				t.Errorf("lint took %v", d)
			}
			log, err := c.Log(ctx, acme, cs[1])
			if err != nil {
				t.Fatal(err)
			}
			if got := gh.FailLine(log); got != "--- FAIL: TestBurst (0.02s)" {
				t.Errorf("the failing line is %q", got)
			}
			if _, err := c.Log(ctx, acme, cs[3]); err == nil {
				t.Error("a commit status has no Actions log to read")
			}

			s.Fail("GET /repos/acme/api/pulls", &gh.Error{Status: 401, Message: "Bad credentials"})
			_, err = c.PRForBranch(ctx, acme, "rate-limits")
			if e, ok := err.(*gh.Error); !ok || e.Status != 401 || e.Message != "Bad credentials" {
				t.Errorf("a refusal comes back as %#v", err)
			}
		})
	}
}

func TestParseRepo(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/acme/api.git":       "acme/api",
		"https://github.com/acme/api":           "acme/api",
		"git@github.com:acme/web-dashboard.git": "acme/web-dashboard",
		"ssh://git@github.com/acme/api.git":     "acme/api",
		"https://gitlab.com/acme/api.git":       "",
		"/tmp/origin.git":                       "",
	} {
		r, ok := gh.ParseRepo(in)
		if got := r.String(); ok != (want != "") || ok && got != want {
			t.Errorf("ParseRepo(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// GitHub's docs, "About code owners": "the last matching pattern takes the most precedence", with gitignore's
// patterns; and "About issue and pull request templates": a template in the root, docs or .github.
func TestTemplateAndOwners(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	if b, f := gh.Template(dir); b != "" || f != "" {
		t.Fatalf("no template: %q %q", b, f)
	}
	write("docs/pull_request_template.md", "docs one")
	write(".github/PULL_REQUEST_TEMPLATE.md", "## What\n\n## Why\n")
	if b, f := gh.Template(dir); b != "## What\n\n## Why\n" || f != ".github/PULL_REQUEST_TEMPLATE.md" {
		t.Errorf("template = %q from %q", b, f)
	}
	write(".github/CODEOWNERS", "# owners\n*       @acme/platform-team\n*.go    @jkowalski\n/web/   @acme/frontend\ndocs/   @writer # anywhere\n")
	for files, want := range map[string][]string{
		"internal/api/handler.go": {"jkowalski"},
		"README":                  {"acme/platform-team"},
		"web/index.html":          {"acme/frontend"},
		"a/docs/x.md":             {"writer"},
		"internal/api/handler.go,web/app.ts,README": {"jkowalski", "acme/frontend", "acme/platform-team"},
	} {
		if got := gh.Owners(dir, strings.Split(files, ",")); !slices.Equal(got, want) {
			t.Errorf("owners of %s = %q, want %q", files, got, want)
		}
	}
}
