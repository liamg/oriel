package catalog

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "tag.gpgSign=false",
		"-c", "commit.gpgSign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// pluginVersions is a plugin's repository with a commit and a tag for each version's files.
type fakeRepo struct {
	dir     string
	commits map[string]string // version → commit
}

func newFakeRepo(t *testing.T, versions [][2]string) fakeRepo {
	t.Helper()
	r := fakeRepo{dir: t.TempDir(), commits: map[string]string{}}
	git(t, r.dir, "init", "-q", "-b", "main")
	for _, v := range versions {
		os.WriteFile(filepath.Join(r.dir, "oriel.toml"), []byte(v[1]), 0o644)
		os.WriteFile(filepath.Join(r.dir, "init.lua"), []byte("oriel.notify(\"hi\")\n"), 0o644)
		git(t, r.dir, "add", ".")
		git(t, r.dir, "commit", "-q", "-m", v[0])
		git(t, r.dir, "tag", "v"+v[0])
		r.commits[v[0]] = git(t, r.dir, "rev-parse", "HEAD")
	}
	return r
}

// fakeGitHub stands in for GitHub: repositories by github.com path, and org members.
type fakeGitHub struct {
	repos   map[string]fakeRepo
	members map[string][]string
}

func (g fakeGitHub) Member(_ context.Context, org, user string) (bool, error) {
	for _, m := range g.members[org] {
		if m == user {
			return true, nil
		}
	}
	return false, nil
}

func (g fakeGitHub) check(t *testing.T, base, head map[string]string, author string) PRReport {
	t.Helper()
	c := PRCheck{Base: writeFiles(t, base), Head: writeFiles(t, head), Author: author, Org: g,
		Fetch: func(_ context.Context, repo, commit string) (string, error) {
			dir := t.TempDir()
			git(t, dir, "clone", "-q", g.repos[repo].dir, ".")
			git(t, dir, "checkout", "-q", "--detach", commit)
			os.RemoveAll(filepath.Join(dir, ".git"))
			return dir, nil
		},
		Tag: func(_ context.Context, repo, tag string) (string, error) {
			out, err := exec.Command("git", "-C", g.repos[repo].dir, "rev-parse", "--verify", "-q", tag+"^{commit}").Output()
			if err != nil {
				return "", nil
			}
			return strings.TrimSpace(string(out)), nil
		}}
	rep, err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

const jira041 = `id = "acme/jira"
version = "0.4.1"
[permissions]
net = { hosts = ["acme.atlassian.net"], why = "Jira" }
`

const jira042 = `id = "acme/jira"
version = "0.4.2"
[permissions]
net = { hosts = ["acme.atlassian.net"], why = "Jira" }
"agent.tools" = { tools = ["jira_ticket"], why = "the agent reads tickets" }
gate = { why = "skip prompts for jira-cli" }
`

func jiraListing(versions ...[2]string) string {
	s := "repo = \"github.com/acme/oriel-jira\"\ndescription = \"Jira tickets in Oriel\"\n"
	for _, v := range versions {
		s += "\n[[versions]]\nversion = \"" + v[0] + "\"\ncommit = \"" + v[1] + "\"\nreviewed = \"https://github.com/liamg/oriel-plugins/pull/1\"\n"
	}
	return s
}

// A fixture PR for a new version gets the comment with the right capability diff. "CI's comment leads with the capability diff and links the GitHub compare. Any gate capability is called out."
func TestPRCommentCapabilityDiff(t *testing.T) {
	repo := newFakeRepo(t, [][2]string{{"0.4.1", jira041}, {"0.4.2", jira042}})
	gh := fakeGitHub{repos: map[string]fakeRepo{"github.com/acme/oriel-jira": repo}}
	c1, c2 := repo.commits["0.4.1"], repo.commits["0.4.2"]
	path := "plugins/acme/jira/plugin.toml"
	rep := gh.check(t, map[string]string{path: jiraListing([2]string{"0.4.1", c1})},
		map[string]string{path: jiraListing([2]string{"0.4.1", c1}, [2]string{"0.4.2", c2})}, "acme")
	if len(rep.Problems) > 0 {
		t.Errorf("problems: %v", rep.Problems)
	}
	head, _, _ := strings.Cut(rep.Comment, "[Source diff")
	for _, want := range []string{
		"## acme/jira 0.4.2\n\n**Capabilities**: 2 more than 0.4.1\n",
		"- `net:acme.atlassian.net`: HTTPS to acme.atlassian.net only\n",
		"- `agent.tools:jira_ticket` **(new)**",
		"- `gate` **(new)**",
		"> ⚠ **gate**: this plugin can veto and decide hooks",
	} {
		if !strings.Contains(head, want) {
			t.Errorf("the comment's lead doesn't have %q:\n%s", want, rep.Comment)
		}
	}
	if want := "[Source diff 0.4.1 → 0.4.2](https://github.com/acme/oriel-jira/compare/" + c1 + "..." + c2 + ")"; !strings.Contains(rep.Comment, want) {
		t.Errorf("no compare link %q:\n%s", want, rep.Comment)
	}
	for _, want := range []string{"| tag | ✓ v0.4.2 → " + c2[:7] + " |", "| pack | ✓ |"} {
		if !strings.Contains(rep.Comment, want) {
			t.Errorf("no %q:\n%s", want, rep.Comment)
		}
	}
}

// A PR for acme/* from someone outside acme fails the owner check: "the PR author is that owner or a member of the org", and "The path matches the repo owner".
func TestPROwnerCheck(t *testing.T) {
	repo := newFakeRepo(t, [][2]string{{"0.4.1", jira041}})
	gh := fakeGitHub{repos: map[string]fakeRepo{"github.com/acme/oriel-jira": repo}, members: map[string][]string{"acme": {"alice"}}}
	head := map[string]string{"plugins/acme/jira/plugin.toml": jiraListing([2]string{"0.4.1", repo.commits["0.4.1"]})}
	if rep := gh.check(t, nil, head, "alice"); len(rep.Problems) > 0 {
		t.Errorf("a member of acme: %v", rep.Problems)
	}
	rep := gh.check(t, nil, head, "mallory")
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0].Text, "mallory isn't acme or a member of it, so can't publish acme/*") {
		t.Errorf("problems: %v", rep.Problems)
	}
	if !strings.Contains(rep.Comment, "### Problems") {
		t.Errorf("the comment doesn't list the problem:\n%s", rep.Comment)
	}
	squat := map[string]string{"plugins/mallory/jira/plugin.toml": head["plugins/acme/jira/plugin.toml"]}
	rep = gh.check(t, nil, squat, "mallory")
	if len(rep.Problems) == 0 || !strings.Contains(rep.Problems[0].Text, "the path's owner mallory isn't the owner of github.com/acme/oriel-jira") {
		t.Errorf("a path that isn't the repo's owner: %v", rep.Problems)
	}
}

// "The tag points at the recorded commit", and "Revoking any version of a trusted plugin also needs
// trusted removed in the same PR."
func TestPRTagAndRevocation(t *testing.T) {
	repo := newFakeRepo(t, [][2]string{{"0.4.1", jira041}, {"0.4.2", jira042}})
	gh := fakeGitHub{repos: map[string]fakeRepo{"github.com/acme/oriel-jira": repo}}
	path := "plugins/acme/jira/plugin.toml"
	rep := gh.check(t, nil, map[string]string{path: jiraListing([2]string{"0.4.1", repo.commits["0.4.2"]})}, "acme")
	if !strings.Contains(rep.Comment, "0.4.1: tag v0.4.1 points at "+repo.commits["0.4.1"][:7]+", not "+repo.commits["0.4.2"][:7]) {
		t.Errorf("a commit the tag doesn't point at: %v", rep.Problems)
	}
	trusted := "repo = \"github.com/acme/oriel-jira\"\ntrusted = true\nsince = \"2026-10-01\"\n"
	rep = gh.check(t, map[string]string{path: trusted}, map[string]string{path: trusted + "\n[revoked]\n\"0.4.1\" = \"stole tokens\"\n"}, "acme")
	if len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0].Text, "needs trusted removed in the same PR") {
		t.Errorf("revoking while trusted: %v", rep.Problems)
	}
}
