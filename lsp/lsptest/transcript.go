// Package lsptest records the JSON-RPC traffic between a client and a real language server, and replays it
// from a fake server, so tests run without the server installed.
//
// A transcript is JSON lines: a header, then one message per line with its direction:
//
//	{"server":"gopls","version":"v0.20.0","root":"${root}"}
//	{"dir":"send","msg":{"jsonrpc":"2.0","id":1,"method":"initialize",…}}
//	{"dir":"recv","msg":{"jsonrpc":"2.0","id":1,"result":{…}}}
//
// "send" is client to server. The project root is written as ${root}, so a transcript replays in any directory.
package lsptest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// RootVar stands for the project root in a transcript.
const RootVar = "${root}"

// Directions.
const (
	Send = "send" // client to server
	Recv = "recv" // server to client
)

// Header is a transcript's first line.
type Header struct {
	Server  string   `json:"server"`
	Version string   `json:"version"`
	Cmd     []string `json:"cmd,omitempty"`
}

// Entry is one message.
type Entry struct {
	Dir string          `json:"dir"`
	Msg json.RawMessage `json:"msg"`
}

// Transcript is a recorded session.
type Transcript struct {
	Header  Header
	Entries []Entry
}

// Load reads a transcript file.
func Load(path string) (*Transcript, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// Parse reads a transcript.
func Parse(b []byte) (*Transcript, error) {
	t := &Transcript{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for n := 0; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if n == 0 {
			if err := json.Unmarshal(line, &t.Header); err != nil {
				return nil, fmt.Errorf("transcript header: %v", err)
			}
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("transcript line %d: %v", n+1, err)
		}
		if e.Dir != Send && e.Dir != Recv {
			return nil, fmt.Errorf("transcript line %d: direction %q", n+1, e.Dir)
		}
		t.Entries = append(t.Entries, e)
	}
	return t, sc.Err()
}

// Write writes a transcript.
func (t *Transcript) Write(w io.Writer) error {
	h, err := json.Marshal(t.Header)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	bw.Write(h)
	bw.WriteByte('\n')
	for _, e := range t.Entries {
		b, err := json.Marshal(e)
		if err != nil {
			return err
		}
		bw.Write(b)
		bw.WriteByte('\n')
	}
	return bw.Flush()
}

// abstract writes root as RootVar in a message.
func abstract(msg []byte, root string) json.RawMessage {
	if root == "" {
		return msg
	}
	// Roots appear bare and inside URIs; a JSON string escapes neither form differently here.
	return json.RawMessage(strings.ReplaceAll(string(msg), root, RootVar))
}

// concrete writes RootVar as root.
func concrete(msg []byte, root string) []byte {
	return []byte(strings.ReplaceAll(string(msg), RootVar, root))
}
