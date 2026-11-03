package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/catalog/registrycmd"
	"github.com/liamg/oriel/integ"
)

const pluginUsage = `oriel plugin pack [-o OUT] [DIR]          check the plugin in DIR (here by default) and build its release archives
                                         into DIR/dist, printing their sha256s
oriel plugin init [--process] [--id OWNER/NAME] [DIR]
                                         start a plugin: oriel.toml and init.lua, and with --process a Go command
                                         under cmd/ that init.lua spawns
oriel plugin publish [--registry OWNER/NAME] [DIR]
                                         propose DIR's tagged version to the registry: a PR through gh
` + registrycmd.Usage

// runPlugin is `oriel plugin …`, for publishing plugins.
func runPlugin(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n%s", pluginUsage)
	}
	if args[0] == "registry" {
		return registrycmd.Run(args[1:], stdout, stderr)
	}
	fs := flag.NewFlagSet("oriel plugin "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, pluginUsage) }
	process := fs.Bool("process", false, "with a process binary, in Go")
	id := fs.String("id", "", "the plugin's id, <owner>/<name>")
	out := fs.String("o", "", "where pack writes the archives (DIR/dist by default)")
	reg := fs.String("registry", catalog.DefaultRepo, "the registry to publish to")
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
	case "publish":
		p, err := catalog.Publish(context.Background(), catalog.PublishOpts{Dir: dir, Registry: *reg, Registrar: catalog.GH{}})
		if err != nil {
			return err
		}
		if p.PR == "" {
			fmt.Fprintln(stdout, p.Note)
			return nil
		}
		fmt.Fprintf(stdout, "%s %s: %s\n", p.ID, p.Version, p.PR)
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

// runPluginProcess runs a bundled plugin's process (plugin.Bundled): its main, by name, over stdin and stdout.
func runPluginProcess(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: oriel plugin-process NAME")
	}
	main, ok := integ.Mains[args[0]]
	if !ok {
		return fmt.Errorf("no bundled process %q", args[0])
	}
	return main(stdin, stdout)
}
