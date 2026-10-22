package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// maxArtifact bounds what an update can make this machine write.
const maxArtifact = 1 << 30

// probeTimeout bounds running a staged binary to ask its version.
const probeTimeout = 30 * time.Second

// Installer stages, swaps and rolls back one installed copy of Oriel: the binary, or the macOS .app bundle.
//
// Dir holds what's staged and the record of a swap not yet confirmed healthy. The previous version is kept beside
// Target (".oriel.prev"), on the same filesystem, so a swap and a rollback are each a rename.
type Installer struct {
	Target string // the installed binary, or .app bundle
	Exe    string // a bundle's executable, relative to Target (Contents/MacOS/oriel); "" when Target is the binary
	Dir    string // the updates directory
	// Probe runs a staged executable before it's accepted, and fails unless it reports version. ProbeVersion if nil.
	Probe func(ctx context.Context, exe, version string) error
}

// Pending is a swap whose new version hasn't yet reported healthy.
type Pending struct {
	Version string    `json:"version"`
	Swapped time.Time `json:"swapped"`
	Boots   int       `json:"boots"` // starts of the new version so far
}

// Rollback is the last version that was swapped in and failed its health check.
type Rollback struct {
	Version string    `json:"version"`
	Reason  string    `json:"reason"`
	At      time.Time `json:"at"`
}

// Outcome is what Boot found.
type Outcome struct {
	Confirmed  string    // the version that just reported healthy after its swap
	RolledBack *Rollback // set when the new version failed and the previous one is back in place: re-exec onto it
}

type staged struct {
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

func (in *Installer) stagedPath() string  { return filepath.Join(in.Dir, "staged") }
func (in *Installer) stagedInfo() string  { return filepath.Join(in.Dir, "staged.json") }
func (in *Installer) pendingPath() string { return filepath.Join(in.Dir, "pending.json") }
func (in *Installer) lastPath() string    { return filepath.Join(in.Dir, "rollback.json") }

func (in *Installer) sibling(suffix string) string {
	return filepath.Join(filepath.Dir(in.Target), "."+filepath.Base(in.Target)+suffix)
}

// Previous is where the version before the last swap is kept.
func (in *Installer) Previous() string { return in.sibling(".prev") }

// Executable is the installed executable: Target, or the one inside the bundle.
func (in *Installer) Executable() string { return in.exeIn(in.Target) }

func (in *Installer) exeIn(root string) string {
	if in.Exe == "" {
		return root
	}
	return filepath.Join(root, in.Exe)
}

// Stage reads a's bytes from r, checks them against the verified manifest m, unpacks a bundle, and runs the result
// to see it start and report m's version. What it stages waits for Swap; anything staged before is replaced.
func (in *Installer) Stage(ctx context.Context, m Manifest, a Artifact, r io.Reader) error {
	return in.stage(ctx, m, a, r, nil)
}

func (in *Installer) stage(ctx context.Context, m Manifest, a Artifact, r io.Reader, p Progress) error {
	if a.Kind == KindBundle && in.Exe == "" {
		return errors.New("a bundle needs the installer's Exe")
	}
	if a.Kind != KindBundle && a.Kind != KindBinary && a.Kind != KindArchive {
		return fmt.Errorf("unknown artifact kind %q", a.Kind)
	}
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(in.Dir, "staging")
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	body, err := readChecked(r, a)
	if err != nil {
		return err
	}
	p.report(Step{Stage: StageExtract, Detail: a.Name})
	switch a.Kind {
	case KindBinary:
		err = os.WriteFile(tmp, body, 0o755)
	case KindArchive:
		var exe []byte
		if exe, err = archiveBinary(body); err == nil {
			err = os.WriteFile(tmp, exe, 0o755)
		}
	default:
		err = untar(body, tmp)
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	probe := in.Probe
	if probe == nil {
		probe = ProbeVersion
	}
	p.report(Step{Stage: StageCheck, Detail: "oriel version says " + m.Version})
	if err := probe(ctx, in.exeIn(tmp), m.Version); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(in.stagedPath()); err != nil {
		return err
	}
	if err := os.Rename(tmp, in.stagedPath()); err != nil {
		return err
	}
	return writeJSON(in.stagedInfo(), staged{Version: m.Version, Kind: a.Kind})
}

// readChecked reads a's bytes, refusing any that don't match its size and checksum.
func readChecked(r io.Reader, a Artifact) ([]byte, error) {
	if a.Size <= 0 || a.Size > maxArtifact {
		return nil, fmt.Errorf("%s: bad size %d", a.Name, a.Size)
	}
	h := sha256.New()
	var buf bytes.Buffer
	n, err := io.Copy(io.MultiWriter(&buf, h), io.LimitReader(r, a.Size+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", a.Name, err)
	}
	if n != a.Size {
		return nil, fmt.Errorf("%s: got %d bytes, want %d", a.Name, n, a.Size)
	}
	if hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return nil, fmt.Errorf("%s: its sha256 doesn't match the manifest", a.Name)
	}
	return buf.Bytes(), nil
}

// untar unpacks a bundle's .tar.gz, whose paths are relative to the bundle, into dir.
func untar(body []byte, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.FromSlash(strings.TrimPrefix(h.Name, "./"))
		if name == "" || name == "." {
			continue
		}
		if !filepath.IsLocal(name) {
			return fmt.Errorf("the bundle has a path outside it: %s", h.Name)
		}
		path := filepath.Join(dir, name)
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(path, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(h.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(f, tr)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			if !filepath.IsLocal(filepath.Join(filepath.Dir(name), h.Linkname)) || filepath.IsAbs(h.Linkname) {
				return fmt.Errorf("the bundle has a link outside it: %s -> %s", h.Name, h.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, path); err != nil {
				return err
			}
		default:
			return fmt.Errorf("the bundle has an entry of an unsupported type: %s", h.Name)
		}
	}
}

// archiveBinary is the executable in a Linux archive: oriel, at its top or in its one top directory.
func archiveBinary(body []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("the archive has no oriel")
		}
		if err != nil {
			return nil, err
		}
		parts := strings.Split(strings.TrimPrefix(h.Name, "./"), "/")
		if h.Typeflag != tar.TypeReg || parts[len(parts)-1] != "oriel" || len(parts) > 2 {
			continue
		}
		if h.Size <= 0 || h.Size > maxArtifact {
			return nil, fmt.Errorf("the archive's oriel has a bad size %d", h.Size)
		}
		return io.ReadAll(io.LimitReader(tr, h.Size))
	}
}

// ProbeVersion runs exe version and fails unless it starts and prints version.
func ProbeVersion(ctx context.Context, exe, version string) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, exe, "version").CombinedOutput()
	first, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	if err != nil {
		return fmt.Errorf("the new version would not run: %v: %s", err, first)
	}
	for _, f := range strings.Fields(first) {
		if strings.TrimPrefix(f, "v") == strings.TrimPrefix(version, "v") {
			return nil
		}
	}
	return fmt.Errorf("the new version reports %q, not %s", first, version)
}

// Staged is the version waiting for Swap, if any.
func (in *Installer) Staged() (string, bool) {
	var s staged
	if readJSON(in.stagedInfo(), &s) != nil {
		return "", false
	}
	if _, err := os.Stat(in.stagedPath()); err != nil {
		return "", false
	}
	return s.Version, true
}

// Swap puts the staged version in Target's place, keeping the one it replaces as Previous, and records the swap as
// pending until Boot confirms it. While an earlier swap is still pending, Previous stays the version before it and
// the unconfirmed one is dropped. Target is never missing: the new copy is moved beside it first, and a failed
// rename puts the old one back.
func (in *Installer) Swap() error {
	version, ok := in.Staged()
	if !ok {
		return errors.New("nothing is staged")
	}
	next, keep := in.sibling(".next"), in.Previous()
	if _, pending := in.Pending(); pending {
		if _, err := os.Stat(keep); err == nil {
			keep = in.sibling(".old")
		}
	}
	if err := os.RemoveAll(next); err != nil {
		return err
	}
	if err := move(in.stagedPath(), next); err != nil {
		return err
	}
	if err := os.RemoveAll(keep); err != nil {
		return err
	}
	if err := os.Rename(in.Target, keep); err != nil {
		return err
	}
	if err := os.Rename(next, in.Target); err != nil {
		_ = os.Rename(keep, in.Target)
		return err
	}
	if keep != in.Previous() {
		_ = os.RemoveAll(keep)
	}
	_ = os.Remove(in.stagedInfo())
	return writeJSON(in.pendingPath(), Pending{Version: version, Swapped: time.Now()})
}

// move renames from to to, copying when they're on different filesystems.
func move(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	fi, err := os.Stat(from)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		err = os.CopyFS(to, os.DirFS(from))
	} else {
		var b []byte
		if b, err = os.ReadFile(from); err == nil {
			err = os.WriteFile(to, b, 0o755)
		}
	}
	if err != nil {
		_ = os.RemoveAll(to)
		return err
	}
	return os.RemoveAll(from)
}

// Pending is the swap waiting for its health check, if any.
func (in *Installer) Pending() (Pending, bool) {
	var p Pending
	return p, readJSON(in.pendingPath(), &p) == nil
}

// Last is the last rollback, if no swap has been confirmed since.
func (in *Installer) Last() (Rollback, bool) {
	var r Rollback
	return r, readJSON(in.lastPath(), &r) == nil
}

// Rollback puts Previous back in Target's place and records why.
func (in *Installer) Rollback(reason string) (Rollback, error) {
	prev := in.Previous()
	if _, err := os.Stat(prev); err != nil {
		return Rollback{}, errors.New("there is no previous version to go back to")
	}
	failed := in.sibling(".failed")
	if err := os.RemoveAll(failed); err != nil {
		return Rollback{}, err
	}
	if err := os.Rename(in.Target, failed); err != nil {
		return Rollback{}, err
	}
	if err := os.Rename(prev, in.Target); err != nil {
		_ = os.Rename(failed, in.Target)
		return Rollback{}, err
	}
	_ = os.RemoveAll(failed)
	p, _ := in.Pending()
	rb := Rollback{Version: p.Version, Reason: reason, At: time.Now()}
	_ = os.Remove(in.pendingPath())
	return rb, writeJSON(in.lastPath(), rb)
}

// Boot is what a start of Oriel runs first. After a swap, the new version gets one start to pass health: if it
// fails, or a start before this one never reported (it crashed or hung), the previous version is put back and the
// caller re-execs onto it. A rollback that can't be done keeps the new version and forgets the swap, so the next
// start doesn't fail the same way. With nothing pending it does nothing.
func (in *Installer) Boot(ctx context.Context, health func(context.Context) error) (Outcome, error) {
	p, ok := in.Pending()
	if !ok {
		return Outcome{}, nil
	}
	if p.Boots > 0 {
		return in.bootRollback("it never reported healthy after its restart")
	}
	p.Boots++
	if err := writeJSON(in.pendingPath(), p); err != nil {
		return Outcome{}, err
	}
	if err := health(ctx); err != nil {
		return in.bootRollback("its health check failed: " + err.Error())
	}
	if err := os.Remove(in.pendingPath()); err != nil {
		return Outcome{}, err
	}
	_ = os.Remove(in.lastPath())
	return Outcome{Confirmed: p.Version}, nil
}

func (in *Installer) bootRollback(reason string) (Outcome, error) {
	rb, err := in.Rollback(reason)
	if rb.At.IsZero() {
		_ = os.Remove(in.pendingPath())
		return Outcome{}, fmt.Errorf("can't roll back (%s): %w", reason, err)
	}
	return Outcome{RolledBack: &rb}, err
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
