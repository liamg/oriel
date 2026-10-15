package ossandbox

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// probeEnv makes this test binary a probe: each argument an operation it tries, each line out "ok" or why not.
const probeEnv = "OSSANDBOX_PROBE"

func TestMain(m *testing.M) {
	Main()
	if os.Getenv(probeEnv) != "" {
		probe(os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func probe(ops []string) {
	for _, op := range ops {
		kind, arg, _ := strings.Cut(op, ":")
		var err error
		switch kind {
		case "read":
			_, err = os.ReadFile(arg)
		case "write":
			err = os.WriteFile(arg, []byte("x"), 0o644)
		case "exec":
			err = exec.Command(arg).Run()
		case "tcp", "udp":
			var c net.Conn
			if c, err = net.Dial(kind, arg); err == nil {
				c.Close()
			}
		}
		if err != nil {
			fmt.Println("denied:", err)
		} else {
			fmt.Println("ok")
		}
	}
}

// run runs the probe confined to p, and returns its answer to each op.
func run(t *testing.T, p Profile, ops ...string) []string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := Command(p, self, ops...)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), probeEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the probe: %v: %s", err, stderr.String())
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != len(ops) {
		t.Fatalf("%d answers to %d ops: %q (%s)", len(lines), len(ops), lines, stderr.String())
	}
	return lines
}

func expect(t *testing.T, ops []string, got []string, want ...bool) {
	t.Helper()
	for i, w := range want {
		if ok := got[i] == "ok"; ok != w {
			t.Errorf("%s: %s, want allowed=%v", ops[i], got[i], w)
		}
	}
}

// A program that isn't there is an error at Command.
func TestCommandNeedsTheProgram(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	if _, err := Command(Profile{}, "/no/such/program"); err == nil {
		t.Error("a missing program")
	}
}
