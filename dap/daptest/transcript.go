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

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
)

// RootVar stands for the project root in a transcript.
const RootVar = "${root}"

// Directions.
const (
	Send = "send" // client to adapter
	Recv = "recv" // adapter to client
)

// Header is a transcript's first line.
type Header struct {
	Adapter string   `json:"adapter"`
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

// Vars are the machine's paths a transcript names by variable: ${root} and others. Longer values are replaced
// first, so a root inside the home directory stays ${root}.
type Vars map[string]string

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

// Requests are the commands the client sent, in order.
func (t *Transcript) Requests() []string {
	var out []string
	for _, e := range t.Entries {
		var m struct{ Type, Command string }
		if e.Dir == Send && json.Unmarshal(e.Msg, &m) == nil && m.Type == "request" {
			out = append(out, m.Command)
		}
	}
	return out
}

func (v Vars) sorted() []string {
	keys := make([]string, 0, len(v))
	for k, val := range v {
		if val != "" {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b string) int { return cmp.Compare(len(v[b]), len(v[a])) })
	return keys
}

// abstract writes the vars' values as their names.
func (v Vars) abstract(msg []byte) json.RawMessage {
	s := string(msg)
	for _, k := range v.sorted() {
		s = strings.ReplaceAll(s, jsonText(v[k]), k)
	}
	return json.RawMessage(s)
}

// concrete writes the vars' names as their values.
func (v Vars) concrete(msg []byte) []byte {
	s := string(msg)
	for k, val := range v {
		s = strings.ReplaceAll(s, k, jsonText(val))
	}
	return []byte(s)
}

// jsonText is s as it appears inside a JSON string.
func jsonText(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// MachineVars are the paths a recording on this machine names by variable: the project root, GOROOT (as the go
// command reports it, else the test binary's) and the home directory.
func MachineVars(root string) Vars {
	v := Vars{RootVar: root, "${goroot}": runtime.GOROOT()}
	if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
		v["${goroot}"] = strings.TrimSpace(string(out))
	}
	if home, err := os.UserHomeDir(); err == nil {
		v["${home}"] = home
	}
	return v
}
