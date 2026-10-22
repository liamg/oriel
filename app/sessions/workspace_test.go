package sessions_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
)

// imageBackend is rig.EchoBackend with an image: it describes itself, and a rebuild writes its log.
type imageBackend struct {
	rig.EchoBackend
	log string
}

func (b imageBackend) Describe(_ context.Context, root string, _ sandbox.Config) sandbox.Description {
	d := sandbox.Description{Container: "oriel-test", Running: true, Image: "oriel-sandbox:abc", ImageFrom: "the base image",
		Dockerfile: "FROM debian:bookworm-slim\nRUN apt-get install -y ripgrep\n", Built: true,
		Places: map[string]string{sandbox.TmpDir(root): "/tmp/claude-501"}}
	if _, err := os.Stat(b.log); err == nil {
		d.BuildLog = b.log
	}
	return d
}

func (b imageBackend) Rebuild(context.Context, string, sandbox.Config) error {
	return os.WriteFile(b.log, []byte("#1 [internal] load build definition\n#5 DONE 2.1s\n"), 0o600)
}

// The Workspace page shows where the agent works and where it finds each directory, browses the scratchpad, shows
// the image's Dockerfile and rebuilds it into the build log, and holds the Permissions.
func TestWorkspacePage(t *testing.T) {
	b := imageBackend{log: filepath.Join(t.TempDir(), "build.log")}
	s, _, se := apptest.SandboxedScene(t, b)
	root := se.Dir
	if p, ok := se.Primary(); root == "" && ok {
		root = p.Dir
	}
	os.MkdirAll(sandbox.TmpDir(root), 0o700)
	if err := os.WriteFile(filepath.Join(sandbox.TmpDir(root), "notes.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Run("workspace.open", nil)
	s.Until("the overview", func() bool {
		return s.Has("SESSION", "SANDBOX", "DIRECTORIES", "Scratchpad", "in the sandbox: /tmp/claude-501", "oriel-test · running",
			"oriel-sandbox:abc · the base image", "TEST · RUNNING")
	})

	s.ClickText("FILES")
	s.Until("the scratchpad's file", func() bool { return s.Has("notes.md") })

	s.ClickText("IMAGE")
	s.Until("the Dockerfile", func() bool { return s.Has("RUN apt-get install -y ripgrep") })
	s.ClickText("Rebuild image")
	s.Until("the build log", func() bool { s.Now = s.Now.Add(time.Second); return s.Has("#5 DONE 2.1s") })

	s.ClickText("PERMISSIONS")
	s.Until("the permissions", func() bool { s.Now = s.Now.Add(time.Second); return s.Has("TOOLS", "NETWORK") })

	if err := s.Registry().Run(context.Background(), "workspace.open", registry.Args{"tab": "nothing"}); err == nil {
		t.Error("opened a tab that isn't there")
	}
}

// Rename… on the overview renames the session in place: its id stays.
func TestWorkspaceRenamesTheSession(t *testing.T) {
	s, _, _ := apptest.SandboxedScene(t, imageBackend{log: filepath.Join(t.TempDir(), "build.log")})
	s.Run("workspace.open", nil)
	s.Until("Rename…", func() bool { return s.Has("Rename…") })
	s.ClickText("Rename…")
	s.Until("Cancel", func() bool { return s.Has("Cancel") })
	s.TypeText("Pricing fixes")
	s.Type("CR")
	s.UntilState("the new name", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && se.Snapshot().Name == "Pricing fixes"
	})
	if st := s.Store().State(); st.Focus != "w" {
		t.Errorf("the session in front is %q, not w", st.Focus)
	}
	s.Until("the name on the page", func() bool { return s.Has("Pricing fixes", "Rename…") })
}
