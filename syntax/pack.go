package syntax

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

//go:embed grammars/PACKS.SHA256SUMS
var packSumsSrc []byte

// PackLimit is the most a grammar pack may hold, unpacked, the same as a plugin's.
const PackLimit = 20 << 20

// ErrPackMismatch is a pack that isn't exactly what the checksum list says (errors.Is).
var ErrPackMismatch = errors.New("grammar pack doesn't match its checksums")

// PackSums are grammar packs' files by pack name, each file's sha256 by its path in the pack.
type PackSums map[string]map[string]string

var packs = sync.OnceValue(func() PackSums {
	s, err := ParsePackSums(packSumsSrc)
	if err != nil {
		panic(err)
	}
	return s
})

// Packs is the checksum list the binary ships: the only packs it installs.
func Packs() PackSums { return packs() }

// ParsePackSums reads a list in sha256sum's form, one "<sha256>  <pack>/<file>" a line.
func ParsePackSums(src []byte) (PackSums, error) {
	out := PackSums{}
	sc := bufio.NewScanner(bytes.NewReader(src))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		sum, name, ok := strings.Cut(line, "  ")
		pack, file, ok2 := strings.Cut(name, "/")
		if b, err := hex.DecodeString(sum); !ok || !ok2 || err != nil || len(b) != sha256.Size || !validPackPath(file) || !validPackName(pack) {
			return nil, fmt.Errorf("pack checksums line %d: %q", n, line)
		}
		if out[pack] == nil {
			out[pack] = map[string]string{}
		}
		out[pack][file] = sum
	}
	return out, sc.Err()
}

// Names are the packs listed, sorted.
func (s PackSums) Names() []string {
	var out []string
	for n := range s {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

func validPackName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsAny(n, `/\`) && !strings.HasPrefix(n, ".")
}

func validPackPath(p string) bool {
	return p != "" && !strings.Contains(p, `\`) && path.Clean(p) == p && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../")
}

// Install unpacks a grammar pack, a .tar.gz of <name>/ holding language.toml, grammar.wasm and queries/, into
// dir/<name>, replacing what was there. Every file must be listed with its checksum and every listed file be
// there; otherwise nothing is written. It returns the pack's name.
func (s PackSums) Install(r io.Reader, dir string) (string, error) {
	files, name, err := s.read(r)
	if err != nil {
		return name, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return name, err
	}
	tmp, err := os.MkdirTemp(dir, ".install-"+name+"-")
	if err != nil {
		return name, err
	}
	defer os.RemoveAll(tmp)
	for p, b := range files {
		f := filepath.Join(tmp, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			return name, err
		}
		if err := os.WriteFile(f, b, 0o644); err != nil {
			return name, err
		}
	}
	dest := filepath.Join(dir, name)
	old := tmp + ".old"
	if err := os.Rename(dest, old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return name, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Rename(old, dest)
		return name, err
	}
	return name, os.RemoveAll(old)
}

// read is the pack's files, checked against s.
func (s PackSums) read(r io.Reader) (map[string][]byte, string, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, "", fmt.Errorf("grammar pack: %w", err)
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	name := ""
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, name, fmt.Errorf("grammar pack: %w", err)
		}
		p := strings.TrimPrefix(strings.TrimSuffix(h.Name, "/"), "./")
		pack, file, _ := strings.Cut(p, "/")
		if !validPackName(pack) || (file != "" && !validPackPath(file)) {
			return nil, name, fmt.Errorf("%w: %q isn't a path in a pack", ErrPackMismatch, h.Name)
		}
		if name == "" {
			name = pack
			if s[name] == nil {
				return nil, name, fmt.Errorf("%w: %q isn't a pack this build of Oriel knows", ErrPackMismatch, name)
			}
		}
		if pack != name {
			return nil, name, fmt.Errorf("%w: %q is outside %s/", ErrPackMismatch, h.Name, name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		default:
			return nil, name, fmt.Errorf("%w: %s isn't a regular file", ErrPackMismatch, p)
		}
		want, ok := s[name][file]
		if !ok {
			return nil, name, fmt.Errorf("%w: %s isn't listed", ErrPackMismatch, p)
		}
		if total += h.Size; h.Size < 0 || total > PackLimit {
			return nil, name, fmt.Errorf("grammar pack %s is over %d MB unpacked", name, PackLimit>>20)
		}
		b, err := io.ReadAll(io.LimitReader(tr, h.Size))
		if err != nil {
			return nil, name, fmt.Errorf("grammar pack: %w", err)
		}
		if sum := sha256.Sum256(b); hex.EncodeToString(sum[:]) != want {
			return nil, name, fmt.Errorf("%w: %s", ErrPackMismatch, p)
		}
		files[file] = b
	}
	if name == "" {
		return nil, "", fmt.Errorf("%w: the archive is empty", ErrPackMismatch)
	}
	for file := range s[name] {
		if _, ok := files[file]; !ok {
			return nil, name, fmt.Errorf("%w: %s/%s is missing", ErrPackMismatch, name, file)
		}
	}
	return files, name, nil
}
