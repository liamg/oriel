package gh_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/gh"
)

func TestTokenNeedsNoGh(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	if tok := gh.Token(context.Background()); tok != "" {
		t.Fatalf("no gh and no variables = %q", tok)
	}
	t.Setenv("GITHUB_TOKEN", "ghp_b")
	if tok := gh.Token(context.Background()); tok != "ghp_b" {
		t.Fatalf("GITHUB_TOKEN = %q", tok)
	}
	t.Setenv("GH_TOKEN", "ghp_a")
	if tok := gh.Token(context.Background()); tok != "ghp_a" {
		t.Fatalf("GH_TOKEN first = %q", tok)
	}
}
