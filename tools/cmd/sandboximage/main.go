// Command sandboximage writes the sandbox's base Dockerfile to a directory and prints the image it's published as,
// for sandbox-image.yml:
//
//	go run ./tools/cmd/sandboximage -dir build    (prints ghcr.io/liamg/oriel-sandbox:<hash>)
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/sandbox/container"
)

func main() {
	dir := flag.String("dir", "", "where to write the Dockerfile; none when empty")
	flag.Parse()
	if *dir != "" {
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(*dir, "Dockerfile"), []byte(container.BaseDockerfile), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Println(container.PublishedBase())
}
