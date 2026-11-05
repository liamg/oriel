package shellenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/isolate"
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

// A shell whose config waits for input, or starts something that never ends, is given up on in time, with what it
// started.
func TestPathGivesUpOnAShellThatHangs(t *testing.T) {
	isolate.Test(t)
	pidfile := filepath.Join(t.TempDir(), "child")
	sh := fakeShell(t, `sleep 60 & echo $! > `+pidfile+`; wait`, "/bin")
	start := time.Now()
	_, err := Path(t.Context(), sh, 300*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "didn't answer") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("gave up after %v", d)
	}
	b, _ := os.ReadFile(pidfile)
	if pid := strings.TrimSpace(string(b)); pid != "" {
		if _, err := os.Stat("/proc/" + pid); err == nil && !strings.HasPrefix(readStat(pid), "Z") {
			t.Errorf("the shell's child %s is still running", pid)
		}
	}
}

// readStat is a process's state letter on Linux; "" elsewhere, or once it's gone.
func readStat(pid string) string {
	b, err := os.ReadFile("/proc/" + pid + "/stat")
	if err != nil {
		return ""
	}
	if i := strings.LastIndex(string(b), ") "); i >= 0 {
		return string(b[i+2:])
	}
	return ""
}

// A shell that can't start a program without a terminal (shiv) is given one.
func TestPathFromAShellThatNeedsATerminal(t *testing.T) {
	isolate.Test(t)
	sh := fakeShell(t, `[ -t 0 ] && [ -t 1 ] || exit 1`, "/opt/tty/bin:/bin")
	if got, err := Path(t.Context(), sh, Timeout); err != nil || got != "/opt/tty/bin:/bin" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

// The shell's directories come first, then what the process had that the shell's lacks, each once.
func TestMerge(t *testing.T) {
	got := Merge("/opt/tools/bin:/usr/bin:/bin", "/usr/bin:/bin:/usr/sbin:/opt/tools/bin:")
	if want := "/opt/tools/bin:/usr/bin:/bin:/usr/sbin"; got != want {
		t.Errorf("Merge = %q, want %q", got, want)
	}
}
