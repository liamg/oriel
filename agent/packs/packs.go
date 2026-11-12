// Package packs holds the prompt packs: what each kind of session tells its agent, and the Oriel tools it expects.
// Built-ins are embedded; plugins and the user's agents/ directory register overrides and new kinds.
package packs

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent"
)

// Tools is every tool Oriel offers an agent, by name: the agent sees each as mcp__oriel__<name>.
var Tools = []string{
	"reply", "thread", "threads", "resolve", "comment", "ask",
	"task_add", "task_update", "tasks", "projects", "repos", "add_project", "new_branch", "rename_branch",
	"commit_draft", "pr_draft", "test_run", "finding",
	"lsp_definition", "lsp_references", "lsp_diagnostics",
}

// GatewayTools are the sandbox gateway's tools a pack may name: the agent sees each as mcp__oriel-gw__<name>.
var GatewayTools = []string{
	"oriel__commit", "oriel__push", "oriel__pull", "oriel__fetch",
	"oriel__pr_create", "oriel__ci", "oriel__repos", "oriel__add_project",
}

//go:embed text
var files embed.FS

var builtin, sources = loadBuiltin()

type entry struct{ p agent.Pack }

// Get returns the built-in pack for a session kind.
func Get(kind string) (agent.Pack, bool) {
	p, ok := builtin[kind]
	return clone(p), ok
}

// Set is the built-in packs with those registered over them: the user's agents/, a plugin's. The zero Set is the
// built-ins alone.
type Set struct {
	mu        sync.Mutex
	overrides []*entry
}

// Get returns the pack for a session kind: the latest registration, else the built-in.
func (s *Set) Get(kind string) (agent.Pack, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(kind)
}

func (s *Set) get(kind string) (agent.Pack, bool) {
	for _, e := range slices.Backward(s.overrides) {
		if e.p.Name == kind {
			return clone(e.p), true
		}
	}
	return Get(kind)
}

// All returns the pack for every kind, built-in or registered, by name.
func (s *Set) All() []agent.Pack {
	s.mu.Lock()
	defer s.mu.Unlock()
	var names []string
	for n := range builtin {
		names = append(names, n)
	}
	for _, e := range s.overrides {
		names = append(names, e.p.Name)
	}
	slices.Sort(names)
	var out []agent.Pack
	for _, n := range slices.Compact(names) {
		p, _ := s.get(n)
		out = append(out, p)
	}
	return out
}

// Register adds a pack, shadowing any built-in or earlier registration of the same name until it is unregistered.
// An empty Skill is the pack's Name.
func (s *Set) Register(p agent.Pack) (unregister func()) {
	if p.Skill == "" {
		p.Skill = p.Name
	}
	e := &entry{clone(p)}
	s.mu.Lock()
	s.overrides = append(s.overrides, e)
	s.mu.Unlock()
	return sync.OnceFunc(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.overrides = slices.DeleteFunc(s.overrides, func(x *entry) bool { return x == e })
	})
}

// LoadDir registers each <name>.md in dir as a pack (the user's ~/.config/oriel/agents/). A file is a pack's
// markdown: frontmatter (name, description, tools) and body, with the include lines expanded, from dir/parts/
// first. Without a name the file's is used; without tools, the Oriel tools its text names. A missing dir loads
// nothing; a bad file loads nothing.
func (s *Set) LoadDir(dir string) (unregister func(), err error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	part := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, "parts", name+".md"))
		return string(b), err == nil && strings.TrimSpace(string(b)) != ""
	}
	var ps []agent.Pack
	for _, ent := range ents {
		if ent.IsDir() || filepath.Ext(ent.Name()) != ".md" {
			continue
		}
		path := filepath.Join(dir, ent.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		p, err := buildWith(string(b), strings.TrimSuffix(ent.Name(), ".md"), part)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		ps = append(ps, p)
	}
	var uns []func()
	for _, p := range ps {
		uns = append(uns, s.Register(p))
	}
	return func() {
		for _, un := range slices.Backward(uns) {
			un()
		}
	}, nil
}

// Meta is a pack's frontmatter.
type Meta struct {
	Name        string
	Description string
	Tools       []string
}

// Parse splits a pack's markdown into its frontmatter and body. A pack's Text is a Claude skill file, frontmatter
// and all; the body alone is what other providers send. Text with no frontmatter is all body.
func Parse(src string) (Meta, string, error) {
	var m Meta
	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		return m, src, nil
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return m, "", errors.New("frontmatter has no closing ---")
	}
	for line := range strings.Lines(head) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return m, "", fmt.Errorf("frontmatter line %q is not key: value", line)
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "name":
			m.Name = v
		case "description":
			m.Description = v
		case "tools":
			for t := range strings.SplitSeq(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					m.Tools = append(m.Tools, t)
				}
			}
		}
	}
	return m, body, nil
}

// FromText is a pack from its markdown, read as LoadDir reads a file: include lines expanded, its tools from the
// frontmatter or the Oriel tools the text names.
func FromText(src, name string) (agent.Pack, error) { return build(src, name) }

// FromTextWith is FromText with the parts it includes looked up in part first: the user's own versions of them.
func FromTextWith(src, name string, part func(name string) (string, bool)) (agent.Pack, error) {
	return buildWith(src, name, part)
}

// Builtins are the names of the built-in packs, sorted.
func Builtins() []string {
	out := make([]string, 0, len(sources))
	for n := range sources {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// Source is a built-in pack's markdown as written, its include lines not yet expanded.
func Source(name string) (string, bool) {
	s, ok := sources[name]
	return s, ok
}

// Parts are the names of the parts a pack can include, sorted.
func Parts() []string {
	ents, _ := files.ReadDir("text/parts")
	var out []string
	for _, e := range ents {
		out = append(out, strings.TrimSuffix(e.Name(), ".md"))
	}
	return out
}

// Part is a built-in part's text.
func Part(name string) (string, bool) {
	b, err := files.ReadFile("text/parts/" + name + ".md")
	if err != nil {
		return "", false
	}
	return string(b), true
}

var include = regexp.MustCompile(`(?m)^<!-- include: ([a-z0-9_-]+) -->$`)

func build(src, name string) (agent.Pack, error) { return buildWith(src, name, nil) }

func buildWith(src, name string, override func(string) (string, bool)) (agent.Pack, error) {
	var missing error
	src = include.ReplaceAllStringFunc(src, func(s string) string {
		part := include.FindStringSubmatch(s)[1]
		if override != nil {
			if t, ok := override(part); ok {
				return strings.TrimRight(t, "\n")
			}
		}
		t, ok := Part(part)
		if !ok {
			missing = fmt.Errorf("no part %q to include", part)
			return s
		}
		return strings.TrimRight(t, "\n")
	})
	if missing != nil {
		return agent.Pack{}, missing
	}
	m, _, err := Parse(src)
	if err != nil {
		return agent.Pack{}, err
	}
	if m.Name != "" {
		name = m.Name
	}
	tools := m.Tools
	if tools == nil {
		tools = mentions(src)
	}
	return agent.Pack{Name: name, Skill: name, Text: src, Tools: tools}, nil
}

var mention = regexp.MustCompile(`mcp__oriel__([a-z0-9_]+)`)

// mentions lists the Oriel tools text names, in the order it first names them.
func mentions(text string) []string {
	var out []string
	for _, m := range mention.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func loadBuiltin() (map[string]agent.Pack, map[string]string) {
	ents, err := files.ReadDir("text")
	if err != nil {
		panic(err)
	}
	out, srcs := map[string]agent.Pack{}, map[string]string{}
	for _, ent := range ents {
		if ent.IsDir() {
			continue
		}
		b, err := files.ReadFile("text/" + ent.Name())
		if err != nil {
			panic(err)
		}
		p, err := build(string(b), strings.TrimSuffix(ent.Name(), ".md"))
		if err != nil {
			panic(fmt.Sprintf("packs: %s: %v", ent.Name(), err))
		}
		out[p.Name] = p
		srcs[p.Name] = string(b)
	}
	return out, srcs
}

func clone(p agent.Pack) agent.Pack {
	p.Tools = slices.Clone(p.Tools)
	return p
}
