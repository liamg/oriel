package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRegistrar is the registry repo on a fake GitHub: its plugin.toml files, and the PRs opened against it.
type fakeRegistrar struct {
	listings map[string]string
	prs      []fakePR
}

type fakePR struct{ branch, path, title, file string }

const prURL = "https://github.com/liamg/oriel-plugins/pull/9"

func (f *fakeRegistrar) Listing(_ context.Context, _, id string) ([]byte, bool, error) {
	s, ok := f.listings[id]
	return []byte(s), ok, nil
}

func (f *fakeRegistrar) OpenPR(_ context.Context, _, branch, path, title, _ string, file func(string) []byte) (string, error) {
	f.prs = append(f.prs, fakePR{branch, path, title, string(file(prURL))})
	return prURL, nil
}

// taggedPlugin is a plugin's repository with its version tagged and pushed to origin, github.com/acme/oriel-jira
// (a local bare repository standing in for it).
func taggedPlugin(t *testing.T, manifest string) (dir, commit string) {
	t.Helper()
	bare := t.TempDir()
	git(t, bare, "init", "-q", "--bare")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+bare+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/acme/oriel-jira")
	dir = t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "oriel.toml"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(dir, "init.lua"), []byte("oriel.notify(\"hi\")\n"), 0o644)
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "v0.4.2")
	git(t, dir, "tag", "v0.4.2")
	git(t, dir, "remote", "add", "origin", "https://github.com/acme/oriel-jira")
	git(t, dir, "push", "-q", "origin", "main", "v0.4.2")
	return dir, git(t, dir, "rev-parse", "HEAD")
}

const jiraManifest = "id = \"acme/jira\"\nversion = \"0.4.2\"\ndescription = \"Jira tickets in Oriel\"\n"

// PL-17 done when: "Against a fake GitHub, publishing a new version and a new plugin each open a PR with the right
// plugin.toml diff." catalog.md: publish "reads oriel.toml and the current tag, then opens the PR through gh (fork
// and branch, as Homebrew's bump-formula-pr does)", and "A new plugin always starts with a reviewed PR."
func TestPublish(t *testing.T) {
	dir, commit := taggedPlugin(t, jiraManifest)
	gh := &fakeRegistrar{listings: map[string]string{}}
	p, err := Publish(context.Background(), PublishOpts{Dir: dir, Registrar: gh})
	if err != nil {
		t.Fatal(err)
	}
	want := `repo        = "github.com/acme/oriel-jira"
description = "Jira tickets in Oriel"

[[versions]]
version  = "0.4.2"
commit   = "` + commit + `"
reviewed = "` + prURL + `"
`
	if len(gh.prs) != 1 || gh.prs[0].file != want || gh.prs[0].path != "plugins/acme/jira/plugin.toml" ||
		gh.prs[0].title != "New plugin: acme/jira 0.4.2" || p.PR != prURL {
		t.Fatalf("a new plugin's PR: %+v\nwant file:\n%s", gh.prs, want)
	}
	if _, err := ParseListing("acme/jira", []byte(gh.prs[0].file)); err != nil {
		t.Errorf("the proposed plugin.toml doesn't parse: %v", err)
	}

	old := "repo        = \"github.com/acme/oriel-jira\"\ndescription = \"Jira tickets in Oriel\"\n\n" +
		"[[versions]]\nversion  = \"0.4.1\"\ncommit   = \"" + strings.Repeat("a", 40) + "\"\n"
	gh = &fakeRegistrar{listings: map[string]string{"acme/jira": old}}
	if _, err := Publish(context.Background(), PublishOpts{Dir: dir, Registrar: gh}); err != nil {
		t.Fatal(err)
	}
	want = "repo        = \"github.com/acme/oriel-jira\"\ndescription = \"Jira tickets in Oriel\"\n\n" +
		"[[versions]]\nversion  = \"0.4.2\"\ncommit   = \"" + commit + "\"\nreviewed = \"" + prURL + "\"\n\n" +
		"[[versions]]\nversion  = \"0.4.1\"\ncommit   = \"" + strings.Repeat("a", 40) + "\"\n"
	if len(gh.prs) != 1 || gh.prs[0].file != want || gh.prs[0].title != "acme/jira 0.4.2" {
		t.Fatalf("a new version's PR: %+v\nwant file:\n%s", gh.prs, want)
	}
	if removed, added := diffLines(old, gh.prs[0].file); removed != 0 || added != 5 {
		t.Errorf("the diff removes %d lines and adds %d; want 0 and 5", removed, added)
	}
}

// PL-17 done when: "On a trusted plugin, publish opens no PR." catalog.md: "For a trusted plugin it checks the tag
// and says there's nothing to publish."
func TestPublishTrusted(t *testing.T) {
	dir, _ := taggedPlugin(t, jiraManifest)
	gh := &fakeRegistrar{listings: map[string]string{"acme/jira": "repo = \"github.com/acme/oriel-jira\"\ntrusted = true\n"}}
	p, err := Publish(context.Background(), PublishOpts{Dir: dir, Registrar: gh})
	if err != nil {
		t.Fatal(err)
	}
	if len(gh.prs) != 0 || p.PR != "" || !strings.Contains(p.Note, "trusted") {
		t.Errorf("a trusted plugin: PRs %v, note %q", gh.prs, p.Note)
	}
	git(t, dir, "tag", "-d", "v0.4.2")
	if _, err := Publish(context.Background(), PublishOpts{Dir: dir, Registrar: gh}); err == nil || !strings.Contains(err.Error(), "isn't tagged v0.4.2") {
		t.Errorf("an untagged HEAD: %v", err)
	}
}

// diffLines counts the lines b removes from a and adds, by longest common subsequence.
func diffLines(a, b string) (removed, added int) {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return len(x) - lcs[0][0], len(y) - lcs[0][0]
}
