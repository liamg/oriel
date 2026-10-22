package catalog

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liamg/oriel/plugin"
)

// AnyPlatform is the archive of a plugin with no binaries.
const AnyPlatform = "any"

// PackOpts shape oriel plugin pack.
type PackOpts struct {
	Out       string   // where the archives go: the plugin's dist/ if ""
	Go        string   // the go command, for a plugin whose binaries are Go: "go" if ""
	Platforms []string // the platforms to build: every one in binaries if nil
}

// Archive is one release archive: <name>-<version>-<os>-<arch>.tar.gz, or <name>-<version>.tar.gz with no
// binaries.
type Archive struct {
	Platform string // "darwin/arm64", or AnyPlatform
	Path     string
	SHA256   string // hex
	Size     int64  // unpacked
}

// Packed is a plugin that passed pack.
type Packed struct {
	Manifest plugin.Manifest
	Archives []Archive
}

// PackError is every reason a plugin didn't pass.
type PackError struct{ Problems []Problem }

func (e *PackError) Error() string {
	lines := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		lines[i] = p.String()
	}
	return strings.Join(lines, "\n")
}

// Pack checks the plugin in dir and builds its release archives: its manifest is valid,
// it has an entry point or contributes something, each statically visible oriel.* call's capability is declared,
// and each archive is within plugin.MaxSize unpacked. A plugin with binaries gets an archive a platform: a Go
// plugin's commands (cmd/*) are built with -trimpath, so the registry's CI can build them again and match;
// another's are taken from bin/<os>-<arch>/.
func Pack(ctx context.Context, dir string, o PackOpts) (Packed, error) {
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		return Packed{}, &PackError{[]Problem{{File: plugin.ManifestFile, Text: err.Error()}}}
	}
	out := o.Out
	if out == "" {
		out = filepath.Join(dir, "dist")
	}
	probs := Check(dir, m)
	files, err := sourceFiles(dir, out)
	if err != nil {
		return Packed{}, err
	}
	if len(probs) > 0 {
		return Packed{Manifest: m}, &PackError{probs}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return Packed{}, err
	}
	_, name, _ := strings.Cut(m.ID, "/")
	p := Packed{Manifest: m}
	platforms := o.Platforms
	if platforms == nil {
		platforms = m.Binaries
	}
	if len(m.Binaries) == 0 {
		platforms = []string{AnyPlatform}
	}
	for _, plat := range platforms {
		fs := files
		file := name + "-" + m.Version + ".tar.gz"
		if plat != AnyPlatform {
			bins, err := binaries(ctx, dir, plat, o.Go)
			if err != nil {
				return Packed{}, fmt.Errorf("%s: %w", plat, err)
			}
			fs = append(append([]entry(nil), files...), bins...)
			file = fmt.Sprintf("%s-%s-%s.tar.gz", name, m.Version, strings.ReplaceAll(plat, "/", "-"))
		}
		a, err := writeArchive(filepath.Join(out, file), fs)
		if err != nil {
			return Packed{}, err
		}
		a.Platform = plat
		if a.Size > plugin.MaxSize {
			probs = append(probs, Problem{Text: fmt.Sprintf("%s is %s unpacked: a plugin may be at most %s", file, mb(a.Size), mb(plugin.MaxSize))})
			os.Remove(a.Path)
			continue
		}
		p.Archives = append(p.Archives, a)
	}
	if len(probs) > 0 {
		return p, &PackError{probs}
	}
	return p, nil
}

// Check is what's wrong with the plugin in dir that pack can see without building it.
func Check(dir string, m plugin.Manifest) []Problem {
	var probs []Problem
	if m.Version == "" {
		probs = append(probs, Problem{File: plugin.ManifestFile, Text: "version is missing"})
	}
	_, err := os.Stat(filepath.Join(dir, plugin.EntryFile))
	hasEntry := err == nil
	if !hasEntry && m.Contributes.Empty() {
		probs = append(probs, Problem{Text: "the plugin has neither an init.lua nor a [contributes] table"})
	}
	for _, p := range m.Contributes.Patterns() {
		if fs, err := plugin.Files(dir, p[1]); err != nil || len(fs) == 0 {
			probs = append(probs, Problem{File: plugin.ManifestFile, Text: fmt.Sprintf("[contributes] %s: %q matches nothing", p[0], p[1])})
		}
	}
	if m.Icon != "" {
		if fs, _ := plugin.Files(dir, m.Icon); len(fs) == 0 {
			probs = append(probs, Problem{File: plugin.ManifestFile, Text: fmt.Sprintf("icon %q isn't in the plugin", m.Icon)})
		}
	}
	und, err := Undeclared(dir, m)
	if err != nil {
		probs = append(probs, Problem{Text: err.Error()})
	}
	return append(probs, und...)
}

func mb(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/(1<<20)) }

// entry is a file in an archive.
type entry struct {
	name string // slash-separated
	data []byte
	exec bool
}

// sourceFiles are the plugin's files, but for .git, out, bin/ and dot files at its root.
func sourceFiles(dir, out string) ([]entry, error) {
	var es []entry
	outReal, _ := filepath.Abs(out)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if abs, _ := filepath.Abs(p); abs == outReal {
			return filepath.SkipDir
		}
		if rel != "." && (strings.HasPrefix(d.Name(), ".") || rel == "bin") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		i, err := d.Info()
		if err != nil {
			return err
		}
		es = append(es, entry{name: rel, data: b, exec: i.Mode()&0o111 != 0})
		return nil
	})
	return es, err
}

// binaries are a platform's process binaries, under bin/.
func binaries(ctx context.Context, dir, plat, goCmd string) ([]entry, error) {
	goos, goarch, _ := strings.Cut(plat, "/")
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		return goBinaries(ctx, dir, goos, goarch, goCmd)
	}
	from := filepath.Join(dir, "bin", goos+"-"+goarch)
	es, err := os.ReadDir(from)
	if err != nil {
		return nil, fmt.Errorf("no go.mod to build from, and no prebuilt binaries in bin/%s-%s", goos, goarch)
	}
	var out []entry
	for _, e := range es {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, entry{name: "bin/" + e.Name(), data: b, exec: true})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("bin/%s-%s is empty", goos, goarch)
	}
	return out, nil
}

// GoBuildFlags are how a plugin's Go commands are built, by pack and by the registry's CI: the same source and Go
// version give the same bytes.
var GoBuildFlags = []string{"build", "-trimpath", "-buildvcs=false", "-ldflags=-buildid="}

// goBinaries builds each command under cmd/ for goos/goarch.
func goBinaries(ctx context.Context, dir, goos, goarch, goCmd string) ([]entry, error) {
	cmds, _ := filepath.Glob(filepath.Join(dir, "cmd", "*", "*.go"))
	seen := map[string]bool{}
	var pkgs []string
	for _, c := range cmds {
		d := filepath.Base(filepath.Dir(c))
		if !seen[d] {
			seen[d] = true
			pkgs = append(pkgs, d)
		}
	}
	if len(pkgs) == 0 {
		return nil, errors.New("binaries are declared, but there's no command under cmd/ to build")
	}
	sort.Strings(pkgs)
	if goCmd == "" {
		goCmd = "go"
	}
	tmp, err := os.MkdirTemp("", "oriel-pack-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	var out []entry
	for _, p := range pkgs {
		bin := filepath.Join(tmp, p)
		args := append(append([]string(nil), GoBuildFlags...), "-o", bin, "./cmd/"+p)
		cmd := exec.CommandContext(ctx, goCmd, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0", "GOFLAGS=-mod=readonly")
		if b, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("building cmd/%s: %v\n%s", p, err, b)
		}
		b, err := os.ReadFile(bin)
		if err != nil {
			return nil, err
		}
		out = append(out, entry{name: "bin/" + p, data: b, exec: true})
	}
	return out, nil
}

// writeArchive writes a reproducible .tar.gz: entries sorted, fixed times, owners and modes.
func writeArchive(path string, es []entry) (Archive, error) {
	sort.Slice(es, func(i, j int) bool { return es[i].name < es[j].name })
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	tw := tar.NewWriter(zw)
	var size int64
	for _, e := range es {
		mode := int64(0o644)
		if e.exec {
			mode = 0o755
		}
		h := &tar.Header{Name: e.name, Mode: mode, Size: int64(len(e.data)), ModTime: time.Unix(0, 0).UTC(),
			Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(h); err != nil {
			return Archive{}, err
		}
		if _, err := tw.Write(e.data); err != nil {
			return Archive{}, err
		}
		size += int64(len(e.data))
	}
	if err := tw.Close(); err != nil {
		return Archive{}, err
	}
	if err := zw.Close(); err != nil {
		return Archive{}, err
	}
	sum := sha256.Sum256(buf.Bytes())
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return Archive{}, err
	}
	return Archive{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: size}, nil
}
