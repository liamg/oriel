// Package lsptest records the JSON-RPC traffic between a client and a real language server, and replays it
// from a fake server, so tests run without the server installed.
//
// A transcript is JSON lines: a header, then one message per line with its direction:
//
//	{"server":"gopls","version":"v0.20.0","root":"${root}"}
//	{"dir":"send","msg":{"jsonrpc":"2.0","id":1,"method":"initialize",…}}
//	{"dir":"recv","msg":{"jsonrpc":"2.0","id":1,"result":{…}}}
//
// "send" is client to server. Paths that name the machine (the project root, GOROOT, the home directory) are
// written as variables, ${root} and so on, so a transcript replays anywhere (internal/transcript).
package lsptest

import "github.com/liamg/oriel/internal/transcript"

// RootVar stands for the project root in a transcript.
const RootVar = transcript.RootVar

// Directions.
const (
	Send = transcript.Send
	Recv = transcript.Recv
)

// Header is a transcript's first line.
type Header struct {
	Server  string   `json:"server"`
	Version string   `json:"version"`
	Cmd     []string `json:"cmd,omitempty"`
}

// Entry is one message.
type Entry = transcript.Entry

// Transcript is a recorded session.
type Transcript = transcript.Transcript[Header]

// Load reads a transcript file.
func Load(path string) (*Transcript, error) { return transcript.Load[Header](path) }

// Parse reads a transcript.
func Parse(b []byte) (*Transcript, error) { return transcript.Parse[Header](b) }
