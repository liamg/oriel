// Command notices writes THIRD_PARTY_NOTICES at the repo's root; with -check it fails if the file is stale.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/liamg/oriel/tools/layout"
	"github.com/liamg/oriel/tools/notices"
)

func main() {
	check := flag.Bool("check", false, "fail if the file isn't what would be written")
	flag.Parse()
	if err := run(*check); err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	wd, _ := os.Getwd()
	root, err := layout.Root(wd)
	if err != nil {
		return err
	}
	parts, err := notices.Parts(root, runtime.GOROOT())
	if err != nil {
		return err
	}
	b, err := notices.Render(root, parts)
	if err != nil {
		return err
	}
	path := filepath.Join(root, notices.File)
	if check {
		old, _ := os.ReadFile(path)
		if !bytes.Equal(old, b) {
			return fmt.Errorf("%s is stale: run go run ./tools/cmd/notices and commit it", notices.File)
		}
		fmt.Printf("notices: %s is current (%d parts)\n", notices.File, len(parts))
		return nil
	}
	return os.WriteFile(path, b, 0o644)
}
