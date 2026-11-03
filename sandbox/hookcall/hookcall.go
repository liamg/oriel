// Package hookcall is `oriel hook <op>`: how Oriel's Claude Code mod hands a decision or an event to the session
// host. It carries one request over the unix socket in ORIEL_HOOK_SOCK and prints the answer. It has no
// dependencies, so the sandbox's own `oriel` serves it too.
package hookcall

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
)

// SocketEnv names the session's hook socket.
const SocketEnv = "ORIEL_HOOK_SOCK"

// Request is one call: its op, and its JSON body.
type Request struct {
	Op   string          `json:"op"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Response is the session's answer, or why it couldn't give one.
type Response struct {
	Body  json.RawMessage `json:"body,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Main sends stdin's JSON to the session in ORIEL_HOOK_SOCK as op, and prints the answer. It returns 0 with the
// answer, 1 when the session couldn't answer, 2 on bad usage.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: oriel hook <op>  (run by Oriel's Claude Code mod, with JSON on stdin)")
		return 2
	}
	sock := os.Getenv(SocketEnv)
	if sock == "" {
		fmt.Fprintln(stderr, "oriel hook: "+SocketEnv+" is not set: this runs inside a session Oriel started")
		return 1
	}
	body, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "oriel hook:", err)
		return 1
	}
	out, err := Call(sock, args[0], body)
	if err != nil {
		fmt.Fprintln(stderr, "oriel hook:", err)
		return 1
	}
	if len(out) == 0 {
		out = []byte("{}")
	}
	_, _ = stdout.Write(append(out, '\n'))
	return 0
}

// Call sends one request and waits for its answer. The connection stays open while it waits: the host reads its
// closing as this process gone.
func Call(sock, op string, body []byte) (json.RawMessage, error) {
	req := Request{Op: op}
	if len(body) > 0 {
		if !json.Valid(body) {
			return nil, fmt.Errorf("stdin is not JSON")
		}
		req.Body = body
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, err
	}
	var res Response
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&res); err != nil {
		return nil, fmt.Errorf("the session closed without answering: %w", err)
	}
	if res.Error != "" {
		return nil, fmt.Errorf("%s", res.Error)
	}
	return res.Body, nil
}
