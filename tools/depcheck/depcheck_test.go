package depcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/internal/gomod"
)

func TestCheck(t *testing.T) {
	allow := Allowlist{"gioui.org": {"v0": true}}
	mods := []Module{
		{Path: "gioui.org", Version: "v0.8.0", From: "a/go.mod"},
		{Path: "github.com/liamg/oriel/text", Version: "v0.0.0", From: "a/go.mod"},
		{Path: "github.com/someone/thing", Version: "v1.0.0", From: "a/go.mod"},
		{Path: "gioui.org", Version: "v2.0.0", From: "b/go.mod"},
		{Path: "github.com/someone/thing", Version: "v1.1.0", From: "b/go.mod"},
	}
	got := Check(allow, mods)
	if len(got) != 2 || !strings.HasPrefix(got[0], "gioui.org v2.0.0") || !strings.HasPrefix(got[1], "github.com/someone/thing v1.0.0") {
		t.Errorf("got %q", got)
	}
}

// The allowlist parses and approves everything the repo builds.
func TestAllowedFile(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, AllowedFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if a, err := gomod.ParseAllowlist(f); err != nil || !a["golang.org/x/sys"]["v0"] {
		t.Fatalf("allowed.txt: %v %v", a, err)
	}
	problems, err := Run(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}
