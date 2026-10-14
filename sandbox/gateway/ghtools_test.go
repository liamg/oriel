package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGHAPI puts a gh on PATH that records its arguments and answers each
// path from routes; an unknown path is a 404.
func fakeGHAPI(t *testing.T, routes map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "argv")
	var b strings.Builder
	b.WriteString("#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + log + "\necho --- >> " + log + "\ncase \"$2\" in\n")
	for path, body := range routes {
		b.WriteString(path + ") cat <<'JSON'\n" + body + "\nJSON\n;;\n")
	}
	b.WriteString("*) echo 'gh: Not Found (HTTP 404)' >&2; exit 1;;\nesac\n")
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func callGH(t *testing.T, h Host, tool string, args any) (string, bool) {
	t.Helper()
	raw, _ := json.Marshal(args)
	res, err := Builtins(h)[tool].Run(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return res.Text(), res.IsError
}

func TestGHRepoVariable(t *testing.T) {
	s := newFakeHost(t)
	log := fakeGHAPI(t, map[string]string{"repos/o/r/actions/variables/VERSION": `{"found":true,"name":"VERSION","value":"1.2"}`})

	out, isErr := callGH(t, s, "oriel__gh_repo_variable", map[string]string{"name": "VERSION", "repo": "o/r"})
	if isErr || !strings.Contains(out, `"value":"1.2"`) {
		t.Errorf("got %q (error %v)", out, isErr)
	}
	out, isErr = callGH(t, s, "oriel__gh_repo_variable", map[string]string{"name": "MISSING", "repo": "o/r"})
	if isErr || out != `{"found":false}` {
		t.Errorf("missing variable: got %q (error %v), want found false", out, isErr)
	}
	if got := argv(t, log); !strings.Contains(got, "--jq") {
		t.Errorf("output not trimmed with --jq, argv:\n%s", got)
	}
}

func TestGHRepoDefaultsToOrigin(t *testing.T) {
	s := newFakeHost(t)
	log := fakeGHAPI(t, map[string]string{"repos/{owner}/{repo}/compare/main...v2": `{"totalCommits":0,"commits":[]}`})

	if out, isErr := callGH(t, s, "oriel__gh_compare", map[string]string{"base": "main", "head": "v2"}); isErr {
		t.Fatalf("compare failed: %s", out)
	}
	if got := argv(t, log); !strings.Contains(got, "repos/{owner}/{repo}/compare/main...v2") {
		t.Errorf("repo not left to gh's origin lookup, argv:\n%s", got)
	}
	if out, isErr := callGH(t, s, "oriel__gh_compare", map[string]string{"base": "a", "head": "b", "repo": "../etc"}); !isErr {
		t.Errorf("bad repo accepted: %s", out)
	}
}

func TestGHWorkflowRunsQuery(t *testing.T) {
	s := newFakeHost(t)
	log := fakeGHAPI(t, map[string]string{"repos/o/r/actions/workflows/release.yml/runs": `[]`})

	if out, isErr := callGH(t, s, "oriel__gh_workflow_runs", map[string]any{"workflow": "release.yml", "status": "completed", "branch": "main", "repo": "o/r"}); isErr {
		t.Fatalf("runs failed: %s", out)
	}
	got := argv(t, log)
	for _, want := range []string{"GET", "per_page=10", "status=completed", "branch=main"} {
		if !strings.Contains(got, want) {
			t.Errorf("argv lacks %q:\n%s", want, got)
		}
	}
}

func TestGHCreateTag(t *testing.T) {
	s := newFakeHost(t)
	log := fakeGHAPI(t, map[string]string{
		"repos/o/r/git/tags": "tagsha",
		"repos/o/r/git/refs": `{"ref":"refs/tags/v1","sha":"tagsha"}`,
	})

	out, isErr := callGH(t, s, "oriel__gh_create_tag", map[string]string{"tag": "v1", "sha": "abc", "message": "v1", "repo": "o/r"})
	if isErr || !strings.Contains(out, "refs/tags/v1") {
		t.Fatalf("got %q (error %v)", out, isErr)
	}
	got := argv(t, log)
	if !strings.Contains(got, "object=abc") || !strings.Contains(got, "sha=tagsha") {
		t.Errorf("annotated tag must point the ref at the tag object, argv:\n%s", got)
	}
}

func TestGHCreateTagRefusesExisting(t *testing.T) {
	s := newFakeHost(t)
	log := fakeGHAPI(t, map[string]string{"repos/o/r/git/ref/tags/v1": "refs/tags/v1"})

	out, isErr := callGH(t, s, "oriel__gh_create_tag", map[string]string{"tag": "v1", "sha": "abc", "repo": "o/r"})
	if !isErr || !strings.Contains(out, "already exists") {
		t.Errorf("got %q (error %v), want a refusal", out, isErr)
	}
	if got := argv(t, log); strings.Contains(got, "POST") {
		t.Errorf("wrote to GitHub for an existing tag, argv:\n%s", got)
	}
}

func argv(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
