// Command oriel-agent serves agenttest's scripted provider over the Oriel agent protocol, by the name Oriel gives it.
package main

import (
	"fmt"
	"os"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/rpcagent"
)

func main() {
	err := rpcagent.Serve(os.Stdin, os.Stdout, func(in rpcagent.Init) (agent.Provider, error) {
		return &agenttest.Scripted{Name: in.Name}, nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
