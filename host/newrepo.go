package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		if _, err := runGit(ctx, filepath.Dir(dir), "clone", "--quiet", "--depth", "1", template, dir); err != nil {
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
	if _, err := runGit(ctx, dir, "init", "--quiet", "-b", "main"); err != nil {
		return err
	}
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	_, err := runGit(ctx, dir, "commit", "--quiet", "-m", "Start "+name)
	if err != nil {
		// git has no identity for the user yet.
		_, err = runGit(ctx, dir, "-c", "user.name=Oriel", "-c", "user.email=oriel@localhost", "commit", "--quiet", "-m", "Start "+name)
	}
	return err
}

func goName(name string) string {
	s := strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.ToLower(name))
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "x" + s
	}
	return s
}
