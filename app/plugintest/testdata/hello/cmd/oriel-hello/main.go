// Command oriel-hello is the test plugin's process: echo answers what it's given and writes it to the plugin's
// data directory.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/agent/jsonrpc"
)

func main() {
	conn := jsonrpc.NewConn(os.Stdin, os.Stdout, jsonrpc.Handlers{Request: func(req *jsonrpc.Request) {
		var p struct {
			Text string `json:"text"`
		}
		json.Unmarshal(req.Params, &p)
		err := os.WriteFile(filepath.Join(os.Getenv("ORIEL_PLUGIN_DATA"), "said"), []byte(p.Text), 0o644)
		req.Reply(map[string]string{"text": p.Text}, err)
	}}, jsonrpc.Options{Version: "2.0"})
	<-conn.Done()
}
