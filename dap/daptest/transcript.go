// Package daptest records the traffic between a DAP client and a real debug adapter, and replays it from a fake
// adapter, so tests run without the adapter installed.
//
// A transcript is JSON lines: a header, then one message per line with its direction:
//
//	{"adapter":"dlv","version":"Delve Debugger 1.27.2","cmd":["dlv","dap","--client-addr=${addr}"]}
//	{"dir":"send","msg":{"seq":1,"type":"request","command":"initialize",…}}
//	{"dir":"recv","msg":{"seq":1,"type":"response","request_seq":1,"success":true,…}}
//
// "send" is client to adapter. Paths that name the machine (the project root, GOROOT, the home directory) are
// written as variables, ${root} and so on, so a transcript replays anywhere.
package daptest

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
	Adapter string   `json:"adapter"`
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

// Vars are the machine's paths a transcript names by variable (transcript.Vars).
type Vars = transcript.Vars

// MachineVars are the paths a recording on this machine names by variable (transcript.MachineVars).
func MachineVars(root string) Vars { return transcript.MachineVars(root) }
