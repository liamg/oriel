package host

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// maxAttach is the largest file a session's agent is handed.
const maxAttach = 32 << 20

// AttachReq is a file for a session's agent: Data (a pasted picture) named Name, or a copy of the file at Path.
type AttachReq struct {
	Session core.SessionID `json:"session"`
	Name    string         `json:"name,omitempty"`
	Data    []byte         `json:"data,omitempty"`
	Path    string         `json:"path,omitempty"`
}

// Attach keeps a file for a session's agent in the session's agent folder, which a sandboxed agent sees too, and
// returns where it is.
func (w *Host) Attach(r AttachReq) (_ string, err error) {
	defer w.recover("Attach", &err)
	if _, err := w.session(r.Session); err != nil {
		return "", err
	}
	name := attachName(r.Name, r.Path)
	if name == "" {
		return "", errors.New("an attachment needs a name or a path")
	}
	dir := sandbox.AttachmentsDir(filepath.Join(w.env.SessionDir(r.Session), "agent"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, hostcore.NewID(6)+"-"+name)
	if r.Path == "" {
		if len(r.Data) > maxAttach {
			return "", fmt.Errorf("%s is over %d MB", name, maxAttach>>20)
		}
		return dst, os.WriteFile(dst, r.Data, 0o644)
	}
	return dst, copyAttachment(r.Path, dst)
}

// attachName is a file's name as it's kept: its base, with what a path or a shell would trip on replaced.
func attachName(name, path string) string {
	if name == "" && path != "" {
		name = filepath.Base(path)
	}
	return strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < ' ' || r == ' ' || r == '(' || r == ')' {
			return '-'
		}
		return r
	}, strings.TrimLeft(name, "."))
}

func copyAttachment(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	switch {
	case fi.IsDir():
		return fmt.Errorf("%s is a folder", filepath.Base(src))
	case fi.Size() > maxAttach:
		return fmt.Errorf("%s is over %d MB", filepath.Base(src), maxAttach>>20)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
