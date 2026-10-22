// Command fake-acp is acptest's fake ACP agent, as a program a plugin names.
package main

import (
	"os"

	"github.com/liamg/oriel/agent/acp/acptest"
)

func main() { os.Exit(acptest.Main()) }
