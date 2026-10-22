//go:build showcase

package sessions_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
)

func TestShowcaseRepos(t *testing.T) {
	s := apptest.ReposScene(t)
	s.Shot("45-repos-page")
	s.ClickText("New tag…")
	s.Until("the tag form", func() bool { return strings.Contains(s.Text(), "Create tag v0.4.3") })
	s.Shot("45-repos-tag")
	s.Run("repos.new", registry.Args{"mode": "clone", "url": "https://github.com/example/my-project"})
	s.Until("the clone target", func() bool {
		s.Now = s.Now.Add(100 * time.Millisecond) // past the fields' rest
		return strings.Contains(s.Text(), "new folder")
	})
	s.Shot("45-repos-add")
}
