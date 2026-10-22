// Package notices makes THIRD_PARTY_NOTICES: the licence of everything built into Oriel that isn't Oriel's, from
// the vendored modules and the parts embedded without Go (libghostty-vt, whisper.cpp, tree-sitter, queries, fonts,
// theme palettes).
package notices

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// File is the notices file, at the repo's root.
const File = "THIRD_PARTY_NOTICES"

// Part is one third-party component and its licence texts.
type Part struct {
	Name    string
	Licence string   // SPDX expression, or "" when only the texts say
	Source  string   // where it comes from
	Texts   []string // licence files, relative to the root unless absolute
}

var licenceFile = regexp.MustCompile(`(?i)^(licen[cs]e|copying|notice|patents)([._-].*)?$`)

// Parts lists every component, in the order the notices give them. goroot is the Go toolchain's, for the standard
// library's licence.
func Parts(root, goroot string) ([]Part, error) {
	parts := []Part{{Name: "Go", Licence: "BSD-3-Clause", Source: "https://go.dev (the standard library and runtime)",
		Texts: []string{filepath.Join(goroot, "LICENSE")}}}
	mods, err := vendored(root)
	if err != nil {
		return nil, err
	}
	parts = append(parts, mods...)
	parts = append(parts,
		Part{Name: "libghostty-vt", Licence: "MIT", Source: "https://github.com/ghostty-org/ghostty (term/ghostty/fetch.sh)",
			Texts: []string{"term/ghostty/licenses/ghostty.txt"}},
		Part{Name: "uucode", Licence: "MIT AND Unicode-3.0", Source: "https://github.com/jacobsandlund/uucode (built into libghostty-vt)",
			Texts: []string{"term/ghostty/licenses/uucode.txt"}},
		Part{Name: "wasi-libc", Licence: "MIT", Source: "https://github.com/WebAssembly/wasi-libc (wasi-sdk-34, linked into each tree-sitter grammar's wasm)",
			Texts: []string{"syntax/grammars/wasi/wasi-libc.txt", "syntax/grammars/wasi/musl.txt"}},
		Part{Name: "simdutf", Licence: "MIT", Source: "https://github.com/simdutf/simdutf (built into the native libghostty-vt)",
			Texts: []string{"term/ghostty/licenses/simdutf.txt"}},
		Part{Name: "Highway", Licence: "BSD-3-Clause", Source: "https://github.com/google/highway (built into the native libghostty-vt)",
			Texts: []string{"term/ghostty/licenses/highway.txt"}},
		Part{Name: "whisper.cpp and ggml", Licence: "MIT", Source: "https://github.com/ggml-org/whisper.cpp (voice/native/fetch.sh)",
			Texts: []string{"voice/licenses/whisper.cpp.txt"}})
	grammars, err := grammars(root)
	if err != nil {
		return nil, err
	}
	parts = append(parts, grammars...)
	parts = append(parts, Part{Name: "nvim-treesitter queries", Licence: "Apache-2.0", Source: source(root, "syntax/queries/SOURCE"),
		Texts: []string{"syntax/queries/LICENSE"}})
	fonts, err := filepath.Glob(filepath.Join(root, "render", "typeset", "fonts", "*", "OFL.txt"))
	if err != nil {
		return nil, err
	}
	slices.Sort(fonts)
	for _, f := range fonts {
		dir := filepath.Dir(f)
		rel, _ := filepath.Rel(root, dir)
		parts = append(parts, Part{Name: "Font: " + filepath.Base(dir), Licence: "OFL-1.1",
			Source: source(root, filepath.ToSlash(filepath.Join(rel, "SOURCE"))), Texts: []string{filepath.ToSlash(filepath.Join(rel, "OFL.txt"))}})
	}
	themes, err := themes(root)
	if err != nil {
		return nil, err
	}
	return append(parts, themes...), nil
}

// attribution is a theme file's first line: "# Name, by Author (Licence): source".
var attribution = regexp.MustCompile(`^# (.+) \(([^()]+)\): (\S+)$`)

// themes are the built-in themes whose palettes are someone else's, by their first line, each with its licence in
// theme/themes/licenses/<theme>.txt.
func themes(root string) ([]Part, error) {
	files, err := filepath.Glob(filepath.Join(root, "theme", "themes", "*.toml"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	var parts []Part
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		first, _, _ := strings.Cut(string(b), "\n")
		m := attribution.FindStringSubmatch(first)
		if m == nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(f), ".toml")
		text := "theme/themes/licenses/" + base + ".txt"
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(text))); err != nil {
			return nil, fmt.Errorf("theme %s: %w", base, err)
		}
		parts = append(parts, Part{Name: "Theme: " + m[1], Licence: m[2], Source: m[3], Texts: []string{text}})
	}
	return parts, nil
}

// vendored is each module in vendor/modules.txt with the licence files in its directory, not counting another
// module's directory inside it.
func vendored(root string) ([]Part, error) {
	b, err := os.ReadFile(filepath.Join(root, "vendor", "modules.txt"))
	if err != nil {
		return nil, err
	}
	type mod struct{ path, version string }
	var mods []mod
	roots := map[string]bool{}
	for l := range strings.Lines(string(b)) {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == "#" && !strings.HasPrefix(f[1], "#") {
			mods = append(mods, mod{f[1], f[2]})
			roots[f[1]] = true
		}
	}
	var parts []Part
	for _, m := range mods {
		dir := filepath.Join(root, "vendor", filepath.FromSlash(m.path))
		var texts []string
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(filepath.Join(root, "vendor"), p)
			if d.IsDir() && p != dir && roots[filepath.ToSlash(rel)] {
				return filepath.SkipDir
			}
			if !d.IsDir() && licenceFile.MatchString(d.Name()) {
				r, _ := filepath.Rel(root, p)
				texts = append(texts, filepath.ToSlash(r))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(texts) == 0 {
			return nil, fmt.Errorf("vendor/%s has no licence file", m.path)
		}
		parts = append(parts, Part{Name: m.path + " " + m.version, Source: "https://" + m.path, Texts: texts})
	}
	return parts, nil
}

// grammars reads syntax/grammars/manifest.tsv: the runtime, then one part per repository, naming its grammars.
func grammars(root string) ([]Part, error) {
	b, err := os.ReadFile(filepath.Join(root, "syntax", "grammars", "manifest.tsv"))
	if err != nil {
		return nil, err
	}
	var parts []Part
	byRepo := map[string]int{}
	for l := range strings.Lines(string(b)) {
		l = strings.TrimRight(l, "\n")
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) < 6 || f[5] == "" {
			return nil, fmt.Errorf("syntax/grammars/manifest.tsv: %q has no licence column", f[0])
		}
		name, repo, rev, licence := f[0], f[1], f[2], f[5]
		if i, ok := byRepo[repo]; ok {
			parts[i].Name += ", " + name
			continue
		}
		p := Part{Name: "tree-sitter grammar: " + name, Licence: licence, Source: "https://github.com/" + repo + " (" + rev + ")",
			Texts: []string{"syntax/grammars/licenses/" + strings.ReplaceAll(repo, "/", "_") + ".txt"}}
		if name == "runtime" {
			p.Name = "tree-sitter runtime"
		}
		byRepo[repo] = len(parts)
		parts = append(parts, p)
	}
	return parts, nil
}

// source is a SOURCE file's "source:" line, with its commit when it has one.
func source(root, rel string) string {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	defer f.Close()
	var src, commit string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, _ := strings.Cut(sc.Text(), ":")
		v = strings.TrimSpace(v)
		switch k {
		case "source":
			src = v
		case "commit":
			commit, _, _ = strings.Cut(v, " ")
		}
	}
	if commit != "" {
		return src + " (" + commit + ")"
	}
	return src
}

const rule = "================================================================================"

// Render writes the notices: each part with its licence texts, a text already given in full pointing back to it.
func Render(root string, parts []Part) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString("Third-party notices for Oriel\n\n" +
		"Oriel includes the software below. Each is under its own licence, given in full here.\n" +
		"Generated by tools/cmd/notices from vendor/ and the embedded parts; do not edit.\n")
	seen := map[string]string{}
	for _, p := range parts {
		fmt.Fprintf(&buf, "\n%s\n%s\n", rule, p.Name)
		if p.Licence != "" {
			fmt.Fprintf(&buf, "Licence: %s\n", p.Licence)
		}
		if p.Source != "" {
			fmt.Fprintf(&buf, "Source: %s\n", p.Source)
		}
		for _, t := range p.Texts {
			path := t
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, filepath.FromSlash(t))
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p.Name, err)
			}
			text := strings.TrimSpace(strings.ReplaceAll(string(b), "\r\n", "\n"))
			if text == "" {
				return nil, errors.New(p.Name + ": " + t + " is empty")
			}
			if first, ok := seen[text]; ok {
				fmt.Fprintf(&buf, "\n%s: the same as %s, above.\n", label(t), first)
				continue
			}
			seen[text] = p.Name
			fmt.Fprintf(&buf, "\n%s:\n\n%s\n", label(t), text)
		}
	}
	return buf.Bytes(), nil
}

// label names a licence file by its base name, or its path under vendor/.
func label(t string) string {
	if filepath.IsAbs(t) {
		return filepath.Base(t)
	}
	if rest, ok := strings.CutPrefix(t, "vendor/"); ok {
		return rest
	}
	return filepath.Base(t)
}
