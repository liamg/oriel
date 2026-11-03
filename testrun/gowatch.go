package testrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
)

// goPackage is what watch needs of `go list -json`.
type goPackage struct {
	Dir          string
	ImportPath   string
	Deps         []string
	TestImports  []string
	XTestImports []string
	TestGoFiles  []string
	XTestGoFiles []string
}

// goGraphCache keeps each workspace's package graph until its go.mod or go.work changes.
type goGraphCache struct {
	mu sync.Mutex
	by map[string]*goGraph
}

type goGraph struct {
	stamp [32]byte
	pkgs  []goPackage
	byDir map[string]*goPackage
	byIP  map[string]*goPackage
	// Lists counts the times go list ran, for tests of the cache.
	lists int
}

// modStamp is a hash of the files whose change means the graph must be listed again.
func modStamp(root string) [32]byte {
	h := sha256.New()
	for _, n := range []string{"go.mod", "go.work"} {
		b, _ := os.ReadFile(filepath.Join(root, n))
		fmt.Fprintf(h, "%s %d\n", n, len(b))
		h.Write(b)
	}
	var s [32]byte
	copy(s[:], h.Sum(nil))
	return s
}

// realDir is dir with its symlinks followed, as far as it can be: go list and the saved file may name it differently.
func realDir(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

// graph is the workspace's packages, listed again when go.mod or go.work changed or when force is set.
func (g *Go) packageGraph(ws Workspace, force bool) (*goGraph, error) {
	g.graph.mu.Lock()
	defer g.graph.mu.Unlock()
	if g.graph.by == nil {
		g.graph.by = map[string]*goGraph{}
	}
	stamp := modStamp(ws.Root)
	old := g.graph.by[ws.Root]
	if old != nil && old.stamp == stamp && !force {
		return old, nil
	}
	cmd := exec.Command(g.bin(), "list", "-e", "-json", "./...")
	cmd.Dir = ws.Root
	cmd.Env = append(os.Environ(), ws.Env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("testrun: go list: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	gr := &goGraph{stamp: stamp, byDir: map[string]*goPackage{}, byIP: map[string]*goPackage{}}
	if old != nil {
		gr.lists = old.lists
	}
	gr.lists++
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		gr.pkgs = append(gr.pkgs, p)
	}
	for i := range gr.pkgs {
		gr.byDir[realDir(gr.pkgs[i].Dir)] = &gr.pkgs[i]
		gr.byIP[gr.pkgs[i].ImportPath] = &gr.pkgs[i]
	}
	g.graph.by[ws.Root] = gr
	return gr, nil
}

// Affected is the packages to test again after file was saved: its own, and every package in the workspace that
// imports it, directly or not, from its code or its tests. Saving go.mod or go.work affects them all.
func (g *Go) Affected(ws Workspace, file string) ([]Item, error) {
	gr, err := g.packageGraph(ws, false)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(file)
	if base == "go.mod" || base == "go.work" || base == "go.sum" {
		var all []Item
		for _, p := range gr.pkgs {
			if len(p.TestGoFiles)+len(p.XTestGoFiles) > 0 {
				all = append(all, goPackageItem(p))
			}
		}
		return all, nil
	}
	if !strings.HasSuffix(file, ".go") {
		return nil, nil
	}
	dir := realDir(filepath.Dir(file))
	changed := gr.byDir[dir]
	if changed == nil {
		// A package that's new since the graph was listed.
		if gr, err = g.packageGraph(ws, true); err != nil {
			return nil, err
		}
		if changed = gr.byDir[dir]; changed == nil {
			return nil, nil
		}
	}
	if strings.HasSuffix(file, "_test.go") {
		return []Item{goPackageItem(*changed)}, nil
	}
	c := changed.ImportPath
	reaches := func(ip string) bool {
		if ip == c {
			return true
		}
		p := gr.byIP[ip]
		return p != nil && slices.Contains(p.Deps, c)
	}
	var out []Item
	for _, p := range gr.pkgs {
		if len(p.TestGoFiles)+len(p.XTestGoFiles) == 0 {
			continue
		}
		hit := reaches(p.ImportPath)
		for _, t := range append(append([]string(nil), p.TestImports...), p.XTestImports...) {
			hit = hit || reaches(t)
		}
		if hit {
			out = append(out, goPackageItem(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out, nil
}

func goPackageItem(p goPackage) Item {
	return Item{Package: p.ImportPath, File: p.Dir, Kind: KindPackage, Path: []string{p.ImportPath}}
}

// Coverage reads a -coverprofile, whose files are named by import path.
func (g *Go) Coverage(ws Workspace, r io.Reader) (*Coverage, error) {
	gr, _ := g.packageGraph(ws, false)
	return parseGoCoverProfile(r, func(name string) string {
		ip, file := pathDir(name)
		if gr != nil {
			if p := gr.byIP[ip]; p != nil {
				return filepath.Join(p.Dir, file)
			}
		}
		if m, ok := g.module(ws.Root); ok && strings.HasPrefix(name, m.path+"/") {
			return filepath.Join(m.root, filepath.FromSlash(strings.TrimPrefix(name, m.path+"/")))
		}
		return name
	})
}

func pathDir(name string) (dir, file string) {
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return "", name
	}
	return name[:i], name[i+1:]
}
