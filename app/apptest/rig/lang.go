package rig

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/lsp/lsptest"
	"github.com/liamg/oriel/registry/lang"
)

// moduleDir is the module's root, for the fixtures in its packages' testdata.
func moduleDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// LSPProject is the lsp module's fixture (lsp/testdata/<fixture>) in a directory of the test's.
func LSPProject(t *testing.T, fixture string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(moduleDir(), "lsp", "testdata", fixture)
	ents, _ := os.ReadDir(src)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(src, e.Name()))
		os.WriteFile(filepath.Join(root, strings.TrimSuffix(e.Name(), ".in")), b, 0o644)
	}
	return root
}

// Recorded answers as the language server it's given to did in fixture's recorded session.
func Recorded(t *testing.T, fixture string) *lsptest.Responder {
	t.Helper()
	tr, err := lsptest.Load(filepath.Join(moduleDir(), "lsp", "testdata", "transcripts", fixture+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return lsptest.NewResponder(tr)
}

// RecordedServer is the built-in language server named server, answering from fixture's recorded session.
func RecordedServer(t *testing.T, fixture, server string) lang.LSPServer {
	t.Helper()
	return ServerAnswering(t, server, Recorded(t, fixture))
}

// ServerAnswering is the built-in language server named server, answered by r.
func ServerAnswering(t *testing.T, server string, r *lsptest.Responder) lang.LSPServer {
	t.Helper()
	for _, spec := range lsp.Builtins() {
		if spec.Name == server {
			spec.Connect = r.Launcher()
			spec.Source = "test"
			return lang.LSPServer(spec)
		}
	}
	t.Fatalf("no built-in server %s", server)
	return lang.LSPServer{}
}

// PricingPackage is a Go package with a passing test, a table test with a failing row, and a test that fails while
// a file outside it (FlipFile) exists; it skips the test without go.
func PricingPackage(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go")
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":        "module example.com/pricing\n\ngo 1.22\n",
		"round.go":      roundGoSrc,
		"round_test.go": strings.Replace(roundTestSrc, "FLIP", FlipFile(dir), 1),
	} {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	return dir
}

// FlipFile is a pricing package's flip file, by its directory: beside it, outside the package.
func FlipFile(dir string) string { return dir + "-flip" }

const roundGoSrc = `package pricing

import "strings"

// RoundHalfEven rounds a decimal string to a whole number, ties to even; negative ties are wrong.
func RoundHalfEven(s string) string {
	switch s {
	case "2.5":
		return "2"
	case "-3.5":
		return "-3"
	}
	return strings.TrimSuffix(s, ".0")
}

func RoundMinor(v int) int { return v }
`

const roundTestSrc = `package pricing

import (
	"os"
	"testing"
)

func TestRoundMinor(t *testing.T) {
	if RoundMinor(5) != 5 {
		t.Fatal("bad")
	}
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"2.5", "2.5", "2"},
		{"-3.5", "-3.5", "-4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RoundHalfEven(c.in); got != c.want {
				t.Errorf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestFlaky(t *testing.T) {
	if _, err := os.Stat("FLIP"); err == nil {
		t.Fatal("flipped")
	}
}
`

// ShellMarks is a program that prints two commands as a shell with OSC 133 and 7 would: one that exits 0, one 1.
const ShellMarks = `printf '\033]7;file://host/tmp/proj\a'
printf '\033]133;A\a$ \033]133;B\aecho ok\n\033]133;C\aok\n\033]133;D;0\a'
printf '\033]133;A\a$ \033]133;B\afalse\n\033]133;C\aoops\nstill oops\n\033]133;D;1\a'
printf '\033]133;A\a$ \033]133;B\a'
exec cat`
