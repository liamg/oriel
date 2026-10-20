package main

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/liamg/oriel/sandbox/hookcall"
)

func TestMain(m *testing.M) {
	if os.Getenv("ORIEL_SANDBOX_RUN_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// The Claude mod runs `oriel hook <op>` inside the sandbox, where oriel is this helper: it must carry the call to
// the session's hook socket.
func TestHookReachesTheSession(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "oh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := dir + "/hook.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		var req hookcall.Request
		_ = json.NewDecoder(c).Decode(&req)
		_ = json.NewEncoder(c).Encode(hookcall.Response{Body: json.RawMessage(`{"op":"` + req.Op + `"}`)})
	}()
	cmd := exec.Command(os.Args[0], "hook", "wait")
	cmd.Env = append(os.Environ(), "ORIEL_SANDBOX_RUN_MAIN=1", hookcall.SocketEnv+"="+sock)
	cmd.Stdin = strings.NewReader("{}")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != `{"op":"wait"}` {
		t.Fatalf("oriel hook wait: %v: %s", err, out)
	}
}
