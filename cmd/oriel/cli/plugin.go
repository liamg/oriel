package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liamg/oriel/catalog"
)

const pluginUsage = `oriel plugin pack [DIR]                  check the plugin in DIR (here by default) and build its release archives
                                         into DIR/dist, printing their sha256s
oriel plugin init [--process] [--id OWNER/NAME] [DIR]
                                         start a plugin: oriel.toml and init.lua, and with --process a Go command
                                         under cmd/ that init.lua spawns`

// runPlugin is `oriel plugin …` (catalog.md, Publishing).
func runPlugin(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n%s", pluginUsage)
	}
	fs := flag.NewFlagSet("oriel plugin "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, pluginUsage) }
	process := fs.Bool("process", false, "with a process binary, in Go")
	id := fs.String("id", "", "the plugin's id, <owner>/<name>")
	out := fs.String("o", "", "where pack writes the archives (DIR/dist by default)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	switch args[0] {
	case "pack":
		p, err := catalog.Pack(context.Background(), dir, catalog.PackOpts{Out: *out})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "%s %s\n", p.Manifest.ID, p.Manifest.Version)
		for _, a := range p.Archives {
			rel, err := filepath.Rel(dir, a.Path)
			if err != nil || strings.HasPrefix(rel, "..") {
				rel = a.Path
			}
			fmt.Fprintf(stdout, "sha256:%s  %s  %s\n", a.SHA256, a.Platform, rel)
		}
		return nil
	case "init":
		if *id == "" {
			*id = guessID(dir)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		files, err := catalog.Scaffold(dir, *id, *process)
		if err != nil {
			return err
		}
		for _, f := range files {
			fmt.Fprintln(stdout, "wrote", filepath.Join(dir, f))
		}
		return nil
	}
	return fmt.Errorf("unknown command %q\nusage:\n%s", args[0], pluginUsage)
}

var githubRemote = regexp.MustCompile(`github\.com[:/]([^/]+)/`)

// guessID is <owner>/<name> for a new plugin in dir: the owner from its GitHub remote, the name from the directory
// without an oriel- prefix.
func guessID(dir string) string {
	abs, _ := filepath.Abs(dir)
	name := strings.TrimPrefix(filepath.Base(abs), "oriel-")
	owner := "me"
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = dir
	if b, err := cmd.Output(); err == nil {
		if m := githubRemote.FindStringSubmatch(string(b)); m != nil {
			owner = m[1]
		}
	}
	return owner + "/" + name
}
