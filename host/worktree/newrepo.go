package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/gitx"
)

// Templates a new repo can start from, besides any git URL (a GitHub template repo).
var Templates = []string{"empty", "go-module", "go-cli"}

// initRepo makes a repo at dir from a template, with one commit, so worktrees can be cut from it.
func initRepo(ctx context.Context, dir, template, name, you string) error {
	if template == "" {
		template = "empty"
	}
	files := map[string]string{"README.md": "# " + name + "\n"}
	module := "github.com/" + you + "/" + name
	switch {
	case template == "empty":
	case template == "go-module":
		files["go.mod"] = "module " + module + "\n\ngo 1.26\n"
		files[name+".go"] = "// Package " + goName(name) + " is " + name + ".\npackage " + goName(name) + "\n"
	case template == "go-cli":
		files["go.mod"] = "module " + module + "\n\ngo 1.26\n"
		files["main.go"] = "// Command " + name + " is " + name + ".\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"" + name + "\")\n}\n"
	case strings.Contains(template, "://") || strings.HasPrefix(template, "git@"):
		if err := gitx.CloneDepth(ctx, template, dir, 1); err != nil {
			return err
		}
		if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
			return err
		}
		files = nil
	default:
		return fmt.Errorf("no template %q: use one of %s, or a git URL", template, strings.Join(Templates, ", "))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for f, body := range files {
		if err := os.WriteFile(filepath.Join(dir, f), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if err := gitx.Init(ctx, dir, "main"); err != nil {
		return err
	}
	g := gitx.Repo{Dir: dir}
	if err := g.Commit(ctx, "Start "+name); err != nil {
		// git has no identity for the user yet.
		g.Env = []string{"GIT_AUTHOR_NAME=Oriel", "GIT_AUTHOR_EMAIL=oriel@localhost", "GIT_COMMITTER_NAME=Oriel", "GIT_COMMITTER_EMAIL=oriel@localhost"}
		return g.Commit(ctx, "Start "+name)
	}
	return nil
}

func goName(name string) string {
	s := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(name))
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "x" + s
	}
	return s
}
