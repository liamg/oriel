package sysload

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestParseCPUTime(t *testing.T) {
	for s, want := range map[string]time.Duration{
		"0:01.23":    1230 * time.Millisecond, // macOS
		"00:00:07":   7 * time.Second,         // procps
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"2-00:00:01": 48*time.Hour + time.Second,
		"12:34.50":   12*time.Minute + 34500*time.Millisecond,
	} {
		if got, ok := parseCPUTime(s); !ok || got != want {
			t.Errorf("parseCPUTime(%q) = %v, %v; want %v", s, got, ok, want)
		}
	}
	for _, s := range []string{"", "7", "a:b", "1:2:3:4"} {
		if _, ok := parseCPUTime(s); ok {
			t.Errorf("parseCPUTime(%q) read", s)
		}
	}
}

func TestDescendants(t *testing.T) {
	all := []Proc{{PID: 1}, {PID: 10, PPID: 1}, {PID: 11, PPID: 10}, {PID: 12, PPID: 11}, {PID: 20, PPID: 1}, {PID: 13, PPID: 10}}
	var got []int
	for _, p := range descendants(all, 10) {
		got = append(got, p.PID)
	}
	if want := []int{10, 11, 13, 12}; !slices.Equal(got, want) {
		t.Errorf("descendants of 10: %v, want %v", got, want)
	}
}

func TestLoads(t *testing.T) {
	prev := []Proc{{PID: 1, CPU: time.Second}, {PID: 2, CPU: time.Second}}
	now := []Proc{{PID: 1, CPU: 2 * time.Second, RSS: 10}, {PID: 2, CPU: 1500 * time.Millisecond, RSS: 99}, {PID: 3, CPU: time.Hour, RSS: 5}}
	got := Loads(prev, now, 2*time.Second)
	if got[0].PID != 1 || got[0].Share != .5 || got[1].PID != 2 || got[1].Share != .25 || got[2].PID != 3 || got[2].Share != 0 {
		t.Errorf("Loads: %+v", got)
	}
}

// sleeperEnv makes this test binary a child that sleeps: macOS hides the environment of its own binaries (sleep).
const sleeperEnv = "SYSLOAD_TEST_SLEEPER"

// sleeperReady is what the sleeper writes once it runs, so its own command line shows.
const sleeperReady = "sleeper ready"

func TestSleeper(t *testing.T) {
	if os.Getenv(sleeperEnv) == "" {
		t.Skip("only a child of TestFamily")
	}
	fmt.Println(sleeperReady)
	io.Copy(io.Discard, os.Stdin) // until its parent is done with it
}

// A child started with a session's variable is in this process's family, with its command line and session.
func TestFamily(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestSleeper$")
	cmd.Env = append(os.Environ(), SessionEnv+"=s-123", sleeperEnv+"=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Process.Kill(); cmd.Wait() })
	for sc := bufio.NewScanner(stdout); ; {
		if !sc.Scan() {
			t.Fatal("the sleeper ended before it ran")
		}
		if sc.Text() == sleeperReady {
			break
		}
	}
	fam, err := Family(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if len(fam) == 0 || fam[0].PID != os.Getpid() || fam[0].RSS == 0 {
		t.Fatalf("family doesn't start with this process: %+v", fam)
	}
	i := slices.IndexFunc(fam, func(p Proc) bool { return p.PID == cmd.Process.Pid })
	if i < 0 {
		t.Fatalf("no child %d in %+v", cmd.Process.Pid, fam)
	}
	if c := fam[i]; c.Command != strings.Join(cmd.Args, " ") || c.Session != "s-123" {
		t.Errorf("the child reads %q in session %q", c.Command, c.Session)
	}
}

func TestDisk(t *testing.T) {
	free, total, err := Disk(t.TempDir())
	if err != nil {
		t.Skip(err)
	}
	if total == 0 || free > total {
		t.Errorf("disk free %d of %d", free, total)
	}
}

func TestParseProcArgs(t *testing.T) {
	b := binary.NativeEndian.AppendUint32(nil, 2)
	b = append(b, "/usr/bin/claude\x00\x00\x00\x00claude\x00--resume\x00HOME=/u\x00ORIEL_SESSION=s-9\x00\x00junk"...)
	args, env := parseProcArgs(b)
	if !slices.Equal(args, []string{"claude", "--resume"}) || !slices.Equal(env, []string{"HOME=/u", "ORIEL_SESSION=s-9"}) || sessionIn(env) != "s-9" {
		t.Errorf("args %q, env %q", args, env)
	}
}
