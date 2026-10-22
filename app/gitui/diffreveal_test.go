package gitui_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
)

// A file asked to be shown that's gone by the time the diff draws doesn't keep the diff drawing: once it's shown or
// known gone, the view asks for no more frames.
func TestDiffRevealOfAGoneFileSettles(t *testing.T) {
	for _, how := range []string{"never there", "deleted", "reverted", "nothing"} {
		t.Run(how, func(t *testing.T) {
			repo := rig.DiffRepo(t)
			r := repo.Dir
			s := apptest.New(t, apptest.Options{WorkDir: r})
			gitOf(s).Wait()
			file := filepath.Join(r, "internal/pricing/round.go")
			switch how {
			case "never there":
				file = filepath.Join(r, "gone.go")
			case "deleted":
				os.Remove(file)
			case "reverted":
				repo.Git("checkout", "--", "internal/pricing/round.go")
			}
			gitOf(s).Reload()
			gitOf(s).Wait()
			s.Run("diff.open", registry.Args{"path": file, "line": 40})
			if how == "nothing" {
				diffOf(s).Reveal(r, "")
			}
			for range 30 {
				s.Frame()
				diffOf(s).Wait()
				s.Now = s.Now.Add(10 * time.Millisecond)
			}
			if !strings.Contains(s.Text(), "currency.go") {
				t.Fatalf("the diff isn't showing:\n%s", s.Text())
			}
			if next := s.UI().NextFrame(); !next.IsZero() && !next.After(s.Now) {
				t.Errorf("settled, the diff still asks for a frame at %v (now %v)", next, s.Now)
			}
		})
	}
}
