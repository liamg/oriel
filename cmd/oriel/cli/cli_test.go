package cli

import (
	"io"
	"testing"

	"github.com/liamg/oriel/core"
)

// SE-1: "each kind starts from ... the CLI"; sessions.md → Starting one: `oriel new "fix stale usage prices" --repo
// pricing --ticket INF-2231`, `oriel review <pr-url>`, `oriel init <name> --template go-cli`; direct is the
// directory itself.
func TestSessionCLISpecs(t *testing.T) {
	const wd = "/src/pricing"
	for _, tt := range []struct {
		cmd  string
		args []string
		want core.SessionSpec
	}{
		{"new", []string{"fix stale usage prices", "--repo", "pricing", "--ticket", "INF-2231"}, core.SessionSpec{Kind: core.KindWork,
			Prompt: "fix stale usage prices", Repos: []core.RepoID{"pricing"}, Ticket: "INF-2231", Provider: "claude"}},
		{"new", []string{"--kind", "plan", "round", "invoices"}, core.SessionSpec{Kind: core.KindPlan, Prompt: "round invoices", Provider: "claude"}},
		{"new", []string{"--kind", "tour", "how does rounding work?"}, core.SessionSpec{Kind: core.KindTour, Prompt: "how does rounding work?",
			Repos: []core.RepoID{wd}, Provider: "claude"}},
		{"new", []string{"--kind", "job", "--provider", "codex", "sweep deps"}, core.SessionSpec{Kind: core.KindJob, Prompt: "sweep deps",
			Repos: []core.RepoID{wd}, Provider: "codex"}},
		{"new", []string{"--kind", "direct", "--provider", ""}, core.SessionSpec{Kind: core.KindDirect, Dir: wd}},
		{"new", []string{"--kind", "scratch"}, core.SessionSpec{Kind: core.KindScratch, Provider: "claude"}},
		{"review", []string{"https://github.com/acme/pricing/pull/7"}, core.SessionSpec{Kind: core.KindReview,
			PR: "https://github.com/acme/pricing/pull/7", Provider: "claude"}},
		{"init", []string{"--template", "go-cli", "invoicer"}, core.SessionSpec{Kind: core.KindNewRepo, Name: "invoicer", Template: "go-cli",
			Provider: "claude"}},
	} {
		got, _, err := sessionSpec(tt.cmd, tt.args, wd, io.Discard)
		if err != nil {
			t.Errorf("oriel %s %q: %v", tt.cmd, tt.args, err)
			continue
		}
		if g, w := fmtSpec(got), fmtSpec(tt.want); g != w {
			t.Errorf("oriel %s %q:\n got %s\nwant %s", tt.cmd, tt.args, g, w)
		}
	}
	if _, _, err := sessionSpec("review", nil, wd, io.Discard); err == nil {
		t.Error("oriel review with no URL")
	}
}

func fmtSpec(s core.SessionSpec) string {
	return string(s.Kind) + "|" + s.Prompt + "|" + s.Ticket + "|" + s.PR + "|" + s.Dir + "|" + s.Name + "|" + s.Template + "|" + s.Provider + "|" +
		func() string {
			out := ""
			for _, r := range s.Repos {
				out += string(r) + ","
			}
			return out
		}()
}
