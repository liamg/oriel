package host

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
)

// A pasted picture, or a copy of a file from outside the session, is kept in the session's agent folder, where a
// sandboxed agent can read it too.
func TestAttachKeepsFilesForTheAgent(t *testing.T) {
	r := newRig(t)
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "x", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	dir := filepath.Join(r.w.env.SessionDir(id), "agent", "attachments")

	p, err := r.w.Attach(AttachReq{Session: id, Name: "Screen Shot (2).png", Data: []byte("png")})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != dir || !strings.HasSuffix(p, "-Screen-Shot--2-.png") {
		t.Errorf("kept at %s", p)
	}
	if b, _ := os.ReadFile(p); string(b) != "png" {
		t.Errorf("it holds %q", b)
	}

	src := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(src, []byte("hi"), 0o644)
	p, err = r.w.Attach(AttachReq{Session: id, Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hi" || filepath.Dir(p) != dir {
		t.Errorf("copied to %s: %q", p, b)
	}

	if _, err := r.w.Attach(AttachReq{Session: id, Path: t.TempDir()}); err == nil {
		t.Error("a folder was attached")
	}
	if _, err := r.w.Attach(AttachReq{Session: "nope", Name: "a.png", Data: []byte("x")}); err == nil {
		t.Error("attached to a session that isn't there")
	}
}
