package shellenv

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/term/pty"
)

// fakeShell writes a shell that runs body, then the command it's given (its last argument) with PATH set to path.
func fakeShell(t *testing.T, body, path string) string {
	t.Helper()
	sh := filepath.Join(t.TempDir(), "fakesh")
	src := "#!/bin/sh\n" + body + "\nfor a; do last=$a; done\nPATH=" + path + "; export PATH\n/bin/sh -c \"$last\"\necho 'logged out'\n"
	if err := os.WriteFile(sh, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return sh
}

// The PATH is the one the shell's config sets, whatever its config prints around it; the shell is told it's being
// read, and runs as an interactive login shell.
func TestPathFromTheLoginShell(t *testing.T) {
	isolate.Test(t)
	sh := fakeShell(t, `echo "Welcome back!"
[ "$ORIEL_RESOLVING_ENVIRONMENT" = 1 ] || exit 3
[ "$1 $2 $3" = "-i -l -c" ] || exit 4
echo "PATH=/not/this"`, "/opt/tools/bin:/usr/bin:/bin")
	got, err := Path(t.Context(), sh, Timeout)
	if err != nil || got != "/opt/tools/bin:/usr/bin:/bin" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

// A shell that won't run interactively without a terminal (csh takes -l only alone) is read as a login shell.
func TestPathWithoutInteractive(t *testing.T) {
	isolate.Test(t)
	sh := fakeShell(t, `[ "$1" = "-i" ] && exit 1`, "/opt/csh/bin:/bin")
	if got, err := Path(t.Context(), sh, Timeout); err != nil || got != "/opt/csh/bin:/bin" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

// hangingShell is a shell whose config starts something that never ends, and the file it writes that child's pid to.
func hangingShell(t *testing.T) (sh, pidfile string) {
	pidfile = filepath.Join(t.TempDir(), "child")
	return fakeShell(t, `sleep 1000 & echo $! > `+pidfile+`; wait`, "/bin"), pidfile
}

// A shell whose config waits for input, or starts something that never ends, is given up on, with what it started:
// one that waited for its child would never return. How soon is TestPathGivesUpOnAShellThatHangsBudget.
func TestPathGivesUpOnAShellThatHangs(t *testing.T) {
	isolate.Test(t)
	sh, pidfile := hangingShell(t)
	_, err := Path(t.Context(), sh, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "didn't answer") {
		t.Fatalf("err = %v", err)
	}
	b, _ := os.ReadFile(pidfile)
	if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && !exited(pid, 10*time.Second) { // the kill is sent, not waited for
		t.Errorf("the shell's child %d is still running", pid)
	}
}

// A shell that can't start a program without a terminal (shiv) is given one.
func TestPathFromAShellThatNeedsATerminal(t *testing.T) {
	isolate.Test(t)
	sh := fakeShell(t, `[ -t 0 ] && [ -t 1 ] || exit 1`, "/opt/tty/bin:/bin")
	if got, err := Path(t.Context(), sh, Timeout); err != nil || got != "/opt/tty/bin:/bin" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

// What the shell printed between the markers is taken only if it reads as a PATH: on its terminal, an error is in
// the same stream.
func TestCheckedRefusesWhatIsntAPath(t *testing.T) {
	for _, bad := range []string{"/bin/sh: printenv: command not found", "printenv: not found", "/usr/bin:\r\n/bin"} {
		if _, err := checked("sh", bad); err == nil {
			t.Errorf("%q was taken as a PATH", bad)
		}
	}
	if got, err := checked("sh", "/opt/homebrew/bin:/usr/bin::/bin"); err != nil || got != "/opt/homebrew/bin:/usr/bin::/bin" {
		t.Errorf("a PATH was refused: %q, %v", got, err)
	}
}

// chattyShell is a shell that goes on writing after it has printed the PATH.
func chattyShell(t *testing.T) string {
	sh := filepath.Join(t.TempDir(), "chatty")
	src := "#!/bin/sh\nfor a; do last=$a; done\nPATH=/opt/chatty/bin:/bin; export PATH\n/bin/sh -c \"$last\"\n" +
		"i=0; while [ $i -lt 20000 ]; do echo \"logging out, line $i of many\"; i=$((i+1)); done\n"
	if err := os.WriteFile(sh, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}
	return sh
}

// A shell that goes on writing after it has printed the PATH (a logout script, job notices) is still answered for, and
// let go of: the terminal is read until it closes, as macOS won't let a process exit with its output unread. How soon
// is TestPathFromAShellThatKeepsWritingBudget.
func TestPathFromAShellThatKeepsWriting(t *testing.T) {
	isolate.Test(t)
	if got, err := Path(t.Context(), chattyShell(t), Timeout); err != nil || got != "/opt/chatty/bin:/bin" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

// A process run on a terminal is seen as one.
func TestPtyStdinIsATerminal(t *testing.T) {
	isolate.Test(t)
	if os.Getenv("ORIEL_SHELLENV_REPORT_TERMINAL") == "1" {
		fmt.Printf("terminal=%v\n", FromTerminal())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestPtyStdinIsATerminal$")
	cmd.Env = append(os.Environ(), "ORIEL_SHELLENV_REPORT_TERMINAL=1")
	p, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, _ := io.ReadAll(p)
	_ = cmd.Wait()
	if !strings.Contains(string(out), "terminal=true") {
		t.Errorf("a pty wasn't taken for a terminal: %q", out)
	}
}

// The shell's directories come first, then what the process had that the shell's lacks, each once.
func TestMerge(t *testing.T) {
	got := Merge("/opt/tools/bin:/usr/bin:/bin", "/usr/bin:/bin:/usr/sbin:/opt/tools/bin:")
	if want := "/opt/tools/bin:/usr/bin:/bin:/usr/sbin"; got != want {
		t.Errorf("Merge = %q, want %q", got, want)
	}
}
