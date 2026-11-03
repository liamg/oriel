package isolate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/gh"
)

func TestTestGivesItsOwnTempDir(t *testing.T) {
	outer := os.TempDir()
	var inner string
	t.Run("a", func(t *testing.T) {
		Test(t)
		inner = os.TempDir()
		Test(t)
		if os.TempDir() != inner {
			t.Fatalf("a second call moved TMPDIR from %s to %s", inner, os.TempDir())
		}
	})
	if inner == outer || os.TempDir() != outer {
		t.Fatalf("TMPDIR %s inside, %s after; %s before", inner, os.TempDir(), outer)
	}
}

// A test's HOME is its own, so state kept under it (Application Support, ~/.local/state) never reaches the user's;
// the Go caches stay where they were.
func TestTestGivesItsOwnHome(t *testing.T) {
	outer, _ := os.UserHomeDir()
	want := os.Getenv("GOCACHE")
	if want == "" {
		cache, _ := os.UserCacheDir()
		want = filepath.Join(cache, "go-build")
	}
	var inner string
	t.Run("a", func(t *testing.T) {
		Test(t)
		inner, _ = os.UserHomeDir()
		if gc := os.Getenv("GOCACHE"); gc != want {
			t.Errorf("GOCACHE %q, want %q", gc, want)
		}
	})
	if inner == outer || strings.HasPrefix(inner, outer+string(os.PathSeparator)+"Library") {
		t.Fatalf("HOME %s inside; %s outside", inner, outer)
	}
	if h, _ := os.UserHomeDir(); h != outer {
		t.Fatalf("HOME %s after; %s before", h, outer)
	}
}

// A test's temporary directories and HOME are named as git and the OS give them back (macOS's /var is
// /private/var), so a path a test builds compares equal to one the code under test reads.
func TestTestsPathsHaveNoSymlinks(t *testing.T) {
	t.Run("a", func(t *testing.T) {
		Test(t)
		home, _ := os.UserHomeDir()
		for _, p := range []string{t.TempDir(), os.TempDir(), home} {
			if real, err := filepath.EvalSymlinks(p); err != nil || real != filepath.Clean(p) {
				t.Errorf("%s resolves to %s (%v)", p, real, err)
			}
		}
	})
}

// A test never reaches the user's own credentials: no GitHub token from the environment, gh's sign-in or git's
// credential helper.
func TestTestHidesTheUsersTokens(t *testing.T) {
	Test(t)
	if tok := gh.TokenFor(context.Background(), "github.com"); tok != "" {
		t.Errorf("a GitHub token (%d bytes) is reachable from a test: %s", len(tok), tokenSource(t))
	}
	if err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Run(); err == nil {
		t.Error("the Keychain is readable from a test")
	}
	for _, v := range credentialVars {
		if os.Getenv(v) != "" {
			t.Errorf("$%s is set in a test", v)
		}
	}
}

// tokenSource says where a token came from, without its value.
func tokenSource(t *testing.T) string {
	var from []string
	for _, v := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if os.Getenv(v) != "" {
			from = append(from, v)
		}
	}
	if out, err := exec.Command("gh", "auth", "token").Output(); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		from = append(from, "gh auth token")
	}
	cmd := exec.Command("git", "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=github.com\n\n")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.Output(); err == nil && strings.Contains(string(out), "password=") {
		from = append(from, "git credential")
	}
	return strings.Join(from, ", ")
}
