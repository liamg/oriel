package gitui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

// explorerAll is the Explorer on All, with currency.go's folder open, in a session on r.
func explorerAll(t *testing.T, readOnly bool) (*apptest.Scene, *gittest.Repo) {
	s, r := explorerScene(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	se := core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Dir: r.Dir,
		Projects: []core.Project{{Name: "p", Dir: r.Dir, Primary: true, ReadOnly: readOnly}}}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	s.Run("explorer.scope", registry.Args{"scope": "all"})
	s.Frame()
	s.Until("the tree", func() bool { s.Frame(); return strings.Contains(s.Text(), "currency.go") })
	return s, r
}

// A file's right-click menu renames it: the name typed replaces the old one, on disk and in the tree.
func TestExplorerRenameFromTheMenu(t *testing.T) {
	s, r := explorerAll(t, false)
	menu := s.RightClick(s.TextPoint("currency.go"))
	for _, want := range []string{"New file…", "New folder…", "Rename…", "Duplicate", "Delete…", "Copy path", "Copy relative path", "Reveal in"} {
		if !strings.Contains(menu, want) {
			t.Errorf("the menu lacks %q:\n%s", want, menu)
		}
	}
	s.ClickText("Rename…")
	s.Until("the rename dialog", func() bool { return strings.Contains(s.Text(), "Rename currency.go") })
	s.TypeText("money_units.go")
	s.Type("CR")
	old, renamed := filepath.Join(r.Dir, "internal/pricing/currency.go"), filepath.Join(r.Dir, "internal/pricing/money_units.go")
	s.Until("the file renamed", func() bool {
		_, err := os.Stat(renamed)
		return err == nil
	})
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("%s is still there", old)
	}
	s.Until("the tree on the new name", func() bool { s.Frame(); return strings.Contains(s.Text(), "money_units.go") })
}

// Delete asks first, then moves the file to the trash.
func TestExplorerDeleteAsksThenTrashes(t *testing.T) {
	s, r := explorerAll(t, false)
	s.RightClick(s.TextPoint("scratch.sql"))
	s.ClickText("Delete…")
	s.Until("the question", func() bool { return strings.Contains(s.Text(), "DELETE SCRATCH.SQL?") })
	p := filepath.Join(r.Dir, "scratch.sql")
	if _, err := os.Stat(p); err != nil {
		t.Fatal("deleted before it was confirmed")
	}
	s.ClickText("Move to trash")
	s.Until("the file gone", func() bool {
		_, err := os.Stat(p)
		return os.IsNotExist(err)
	})
}

// New file in a folder makes it and opens it; Duplicate copies a file beside it; Copy relative path copies it from
// the repo's root.
func TestExplorerNewDuplicateAndCopyPath(t *testing.T) {
	s, r := explorerAll(t, false)
	dir := filepath.Join(r.Dir, "internal/pricing")
	s.Run("explorer.new-file", registry.Args{"path": dir})
	s.Until("the new file dialog", func() bool { return strings.Contains(s.Text(), "New file") })
	s.TypeText("tax.go")
	s.Type("CR")
	s.Until("the new file, the dialog closed", func() bool {
		_, err := os.Stat(filepath.Join(dir, "tax.go"))
		return err == nil && !strings.Contains(s.Text(), "New file")
	})
	s.Run("explorer.duplicate", registry.Args{"path": filepath.Join(dir, "currency.go")})
	if b, err := os.ReadFile(filepath.Join(dir, "currency copy.go")); err != nil || !strings.Contains(string(b), "type Currency") {
		t.Errorf("the copy: %q, %v", b, err)
	}
	s.RightClick(s.TextPoint("currency.go"))
	s.ClickText("Copy relative path")
	s.Frame()
	if s.Desktop.Text != "internal/pricing/currency.go" {
		t.Errorf("copied %q", s.Desktop.Text)
	}
}

// In a project the session only reads, the menu's changes are greyed with why, and their commands refuse.
func TestExplorerReadOnlyRefusesChanges(t *testing.T) {
	s, r := explorerAll(t, true)
	menu := s.RightClick(s.TextPoint("currency.go"))
	if !strings.Contains(menu, "the session only reads it") {
		t.Errorf("the menu doesn't say why:\n%s", menu)
	}
	if n := s.Node("Rename…"); n == nil || !n.Disabled {
		t.Errorf("Rename… isn't disabled: %+v", n)
	}
	s.Type("Esc")
	p := filepath.Join(r.Dir, "internal/pricing/currency.go")
	err := s.Registry().Run(t.Context(), "explorer.rename", registry.Args{"path": p, "name": "x.go"})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("renaming a read-only file: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("the read-only file was renamed")
	}
}
