package layout

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
)

// Rule says no package under From reaches a package under Never, through any chain of imports, tests included;
// packages under Except are left to rules of their own. Paths are relative to Module.
type Rule struct {
	From   []string
	Never  []string
	Except []string
}

// Rules are the layering every change keeps (docs/architecture.md).
var Rules = []Rule{
	{From: []string{"text", "edit", "vim", "syntax", "lsp", "dap", "jsonrpc", "core", "agent", "term"}, Never: []string{"ui", "render"}},
	{From: []string{"plugin"}, Never: []string{"ui/widget"}},
	// The registry is pure contributions; what's drawn is registry/uireg's, language tooling registry/lang's, and the
	// built-in services' contracts, made of the editor, git, watches and agents, registry/services'.
	{From: []string{"registry"}, Never: []string{"ui", "render", "lsp", "dap", "syntax", "gitx", "watch", "agent"},
		Except: []string{"registry/uireg", "registry/lang", "registry/services"}},
	{From: []string{"registry/uireg"}, Never: []string{"lsp", "dap"}},
	{From: []string{"registry/lang"}, Never: []string{"ui", "render"}},
	// The session host's parts meet only in its facade, each seeing the host as a host/hostcore State.
	apart("host/agentrun"), apart("host/aireview"), apart("host/github"), apart("host/jobs"), apart("host/persist"),
	apart("host/pricing"), apart("host/repos"), apart("host/sandboxhost"), apart("host/utility"), apart("host/worktree"),
	{From: []string{"host/hostcore", "host/hosttest"}, Never: hostParts},
	// host/stats is a library the parts and the app share: it reads logs and counts, and reaches no part.
	{From: []string{"host/stats"}, Never: append([]string{"app", "ui", "render"}, hostParts...)},
	// Libraries the host and the app both use, reaching neither.
	{From: []string{"search", "sysload"}, Never: []string{"app", "host", "ui", "render"}},
}

// hostParts are the session host's parts (docs/architecture.md, "Layering").
var hostParts = []string{"host/agentrun", "host/aireview", "host/github", "host/jobs", "host/persist", "host/pricing",
	"host/repos", "host/sandboxhost", "host/utility", "host/worktree"}

// apart keeps a part of the host from reaching any other.
func apart(part string) Rule {
	return Rule{From: []string{part}, Never: slices.DeleteFunc(slices.Clone(hostParts), func(p string) bool { return p == part })}
}

// Known are violations still being removed: "from -> target". Each may only go; the test fails on one that no
// longer happens, so it's taken off.
var Known = []string{}

// Package is one package's imports, as go list reports them.
type Package struct {
	ImportPath   string
	Imports      []string
	Deps         []string
	TestImports  []string
	XTestImports []string
}

// List runs go list over every package in the module at root.
func List(root string) ([]Package, error) {
	cmd := exec.Command("go", "list", "-json=ImportPath,Imports,Deps,TestImports,XTestImports", "./...")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	var pkgs []Package
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for dec.More() {
		var p Package
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}

// Violation is a package reaching what its rule forbids.
type Violation struct {
	From, Target string // the rule's entries
	Package, Via string // the package, and the forbidden package it reaches
}

func (v Violation) Key() string { return v.From + " -> " + v.Target }

func (v Violation) String() string {
	return fmt.Sprintf("%s reaches %s, but %s never reaches %s", v.Package, v.Via, v.From, v.Target)
}

func under(path, dir string) bool {
	p := Module + "/" + dir
	return path == p || strings.HasPrefix(path, p+"/")
}

// Check reports every package that breaks a rule, once per rule entry pair and package.
func Check(pkgs []Package, rules []Rule) []Violation {
	deps := map[string][]string{}
	for _, p := range pkgs {
		deps[p.ImportPath] = p.Deps
	}
	var out []Violation
	for _, p := range pkgs {
		reach := slices.Clone(p.Deps)
		for _, t := range slices.Concat(p.TestImports, p.XTestImports) {
			reach = append(append(reach, t), deps[t]...)
		}
		slices.Sort(reach)
		reach = slices.Compact(reach)
		for _, r := range rules {
			if slices.ContainsFunc(r.Except, func(e string) bool { return under(p.ImportPath, e) }) {
				continue
			}
			for _, from := range r.From {
				if !under(p.ImportPath, from) {
					continue
				}
				for _, target := range r.Never {
					for _, d := range reach {
						if under(d, target) {
							out = append(out, Violation{From: from, Target: target, Package: p.ImportPath, Via: d})
							break
						}
					}
				}
			}
		}
	}
	return out
}

// shared are the app's packages every feature may import: what they share, and the test harnesses.
var shared = []string{"kit", "apptest", "plugintest"}

// featureOf is the app/<feature> directory path is under, and whether it's one: "stats" for app/stats/x. The root
// app and the shared packages aren't features.
func featureOf(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, Module+"/app/")
	if !ok {
		return "", false
	}
	f, _, _ := strings.Cut(rest, "/")
	return f, !slices.Contains(shared, f)
}

// FeatureImports reports each package under app/<feature> importing, tests included, the root app or another
// feature (docs/architecture.md, "Layering"): features meet only through the registry and app/kit.
func FeatureImports(pkgs []Package) []Violation {
	var out []Violation
	for _, p := range pkgs {
		from, ok := featureOf(p.ImportPath)
		if !ok {
			continue
		}
		for _, imp := range slices.Concat(p.Imports, p.TestImports, p.XTestImports) {
			to, isFeature := featureOf(imp)
			if imp == Module+"/app" || isFeature && to != from {
				out = append(out, Violation{From: "app/" + from, Target: strings.TrimPrefix(imp, Module+"/"), Package: p.ImportPath, Via: imp})
			}
		}
	}
	return out
}
