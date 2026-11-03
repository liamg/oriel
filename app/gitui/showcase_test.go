//go:build showcase

package gitui_test

import (
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

func TestShowcaseExplorer(t *testing.T) {
	s, r := apptest.ExplorerScene(t)
	s.Run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "internal/pricing/round.go")})
	s.SettleSigns()
	s.Press("<M-3>")
	s.Press("<M-1>")
	s.Shot("01-explorer-changed")
	s.Press("<M-2>")
	s.Shot("02-explorer-uncommitted")
	s.Press("<M-3>")
	s.Shot("03-explorer-all")
}
