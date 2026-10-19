// Command oriel-gitlab is the GitLab plugin's process: it answers the plugin's Lua over JSON-RPC on stdin and
// stdout, and reaches GitLab through the host, which holds the token.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/integ/gitlab"
)

func main() {
	if err := gitlab.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
