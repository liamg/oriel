// Package prompts is every prompt the user can edit on the Prompts page: the coding style, each session kind's
// prompt pack and the parts packs include, and any a plugin registers. Only the user's changes are kept, each in
// a file of its own under the config dir, so a new release still improves the prompts left alone.
package prompts

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/packs"
	"github.com/liamg/oriel/internal/atomicfile"
)

// StyleID is the coding style: written into the workspace's generated CLAUDE.md, and {{style}} in any prompt.
const StyleID = "style.code"

// Groups, in the order the page shows them.
const (
	GroupStyle = "Style"
	GroupPacks = "Session packs"
	GroupParts = "Pack parts"
)

// PackID and PartID are the prompts of a pack and of a part.
func PackID(name string) string { return "pack." + name }
func PartID(name string) string { return "part." + name }

// Def is one editable prompt: what it's for, the placeholders filled in, and the text used while the user hasn't
// written their own.
type Def struct {
	ID    string
	Group string
	Label string
	Help  string // when it's sent and what the agent does with it
	Vars  []Var  // the {{name}} placeholders filled in; an unknown one is left as written
	// Default is the built-in text.
	Default string
	// File is where the user's version is kept, relative to the config dir.
	File string
}

// A Def is a contribution: a plugin registers its prompts with its window (Prepare checks one first).
func (d Def) ContributionKind() string { return "prompt" }
func (d Def) ContributionName() string { return d.ID }

// Var is a placeholder and what's put there.
type Var struct {
	Name string
	Desc string
}

//go:embed defaults/*.md
var defaultFiles embed.FS

var builtin = mustLoad()

func mustLoad() []Def {
	out, err := loadDefaults()
	if err != nil {
		panic("prompts: " + err.Error())
	}
	return out
}

// Defs is every prompt, in the page's order: the defaults, the packs, their parts, then those registered (a
// window's contributions).
func Defs(registered ...Def) []Def {
	out := slices.Clone(builtin)
	out = append(out, packDefs()...)
	for _, d := range registered {
		if !slices.ContainsFunc(out, func(o Def) bool { return o.ID == d.ID }) {
			out = append(out, d)
		}
	}
	return out
}

// Lookup finds a prompt by id, built in or among those registered.
func Lookup(id string, registered ...Def) (Def, bool) {
	for _, d := range Defs(registered...) {
		if d.ID == id {
			return d, true
		}
	}
	return Def{}, false
}

// Prepare is d ready to register beside those already registered: its file, group and label filled in. One with
// no id or text, or an id already taken, is refused.
func Prepare(d Def, registered ...Def) (Def, error) {
	if d.ID == "" || strings.TrimSpace(d.Default) == "" {
		return Def{}, errors.New("a prompt needs an id and its default text")
	}
	if _, ok := Lookup(d.ID, registered...); ok {
		return Def{}, fmt.Errorf("there's a prompt %q already", d.ID)
	}
	if d.File == "" {
		d.File = filepath.Join("prompts", d.ID+".md")
	}
	if d.Group == "" {
		d.Group = "Plugins"
	}
	if d.Label == "" {
		d.Label = d.ID
	}
	return d, nil
}

// packDefs are the built-in packs and the parts they include, as prompts.
func packDefs() []Def {
	var out []Def
	users := map[string][]string{}
	for _, name := range packs.Builtins() {
		src, _ := packs.Source(name)
		m, body, _ := packs.Parse(src)
		out = append(out, Def{ID: PackID(name), Group: GroupPacks, Label: heading(body, "# ", name), Default: src,
			Help: m.Description + " Its include lines pull in the parts below.", File: filepath.Join("agents", name+".md")})
		for _, p := range packs.Parts() {
			if strings.Contains(src, "<!-- include: "+p+" -->") {
				users[p] = append(users[p], name)
			}
		}
	}
	for _, p := range packs.Parts() {
		src, _ := packs.Part(p)
		help := "Included in no pack."
		if len(users[p]) > 0 {
			help = "Included in the " + strings.Join(users[p], ", ") + " packs."
		}
		out = append(out, Def{ID: PartID(p), Group: GroupParts, Label: heading(src, "## ", p), Default: src, Help: help,
			File: filepath.Join("agents", "parts", p+".md")})
	}
	return out
}

// heading is the first line of text with the prefix, without it; or else.
func heading(text, prefix, or string) string {
	for line := range strings.Lines(text) {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			return h
		}
	}
	return or
}

// Overrides are the user's own versions, by prompt id. A prompt they left alone isn't there.
type Overrides map[string]string

// Text is the prompt as it stands: the user's version when there's one, else the default, of the built-ins or
// those registered. An unknown id is "".
func (o Overrides) Text(id string, registered ...Def) string {
	if s, ok := o[id]; ok && strings.TrimSpace(s) != "" {
		return s
	}
	d, _ := Lookup(id, registered...)
	return d.Default
}

// Custom reports whether the user has their own version of a prompt.
func (o Overrides) Custom(id string) bool { return strings.TrimSpace(o[id]) != "" }

// Style is the coding style as it stands, trimmed.
func (o Overrides) Style() string { return strings.TrimSpace(o.Text(StyleID)) }

// Render is the prompt with each {{name}} replaced by vars[name], and {{style}} by the coding style unless vars
// gives one. A placeholder with no value is left as written, so a typo shows.
func (o Overrides) Render(id string, vars map[string]string, registered ...Def) string {
	return o.fill(o.Text(id, registered...), id, vars)
}

func (o Overrides) fill(text, id string, vars map[string]string) string {
	for k, v := range vars {
		text = strings.ReplaceAll(text, "{{"+k+"}}", v)
	}
	if id != StyleID {
		text = strings.ReplaceAll(text, "{{style}}", o.Style())
	}
	return text
}

// Pack is a session kind's pack with the user's versions in: a built-in pack they changed, or whose parts they
// changed, is built from their text; any other comes from base (the built-in one when base is nil). {{style}} in a
// pack is the coding style.
func (o Overrides) Pack(name string, base func(string) (agent.Pack, bool)) (agent.Pack, bool) {
	src, ok := packs.Source(name)
	if ok && base != nil && !o.Custom(PackID(name)) && !o.partsCustom(src) {
		ok = false
	}
	if !ok {
		if base == nil {
			return agent.Pack{}, false
		}
		p, ok := base(name)
		if ok {
			p.Text = o.fill(p.Text, "", nil)
		}
		return p, ok
	}
	if o.Custom(PackID(name)) {
		src = o[PackID(name)]
	}
	p, err := packs.FromTextWith(src, name, func(part string) (string, bool) {
		s, ok := o[PartID(part)]
		return s, ok && strings.TrimSpace(s) != ""
	})
	if err != nil {
		// The user's text doesn't build (a part it names is gone): the built-in pack, rather than none.
		p, ok = packs.Get(name)
		if !ok {
			return agent.Pack{}, false
		}
	}
	p.Text = o.fill(p.Text, "", nil)
	return p, true
}

// partsCustom reports whether the user changed a part src includes.
func (o Overrides) partsCustom(src string) bool {
	for _, p := range packs.Parts() {
		if o.Custom(PartID(p)) && strings.Contains(src, "<!-- include: "+p+" -->") {
			return true
		}
	}
	return false
}

// Store keeps the user's versions as files under a config dir, one per prompt they changed (Def.File), of the
// built-ins and those Registered.
type Store struct {
	Dir        string
	Registered []Def
}

// Path is where a prompt's version is kept.
func (s Store) Path(id string) (string, error) {
	d, ok := Lookup(id, s.Registered...)
	if !ok {
		return "", fmt.Errorf("no prompt %q", id)
	}
	return filepath.Join(s.Dir, d.File), nil
}

// Load reads the user's versions. A file that's empty, or the same as the default, isn't one.
func (s Store) Load() Overrides {
	out := Overrides{}
	for _, d := range Defs(s.Registered...) {
		b, err := os.ReadFile(filepath.Join(s.Dir, d.File))
		if err != nil {
			continue
		}
		if t := string(b); strings.TrimSpace(t) != "" && !same(t, d.Default) {
			out[d.ID] = t
		}
	}
	return out
}

// Set keeps the user's version of a prompt. Text that's empty or the default is a reset: the file goes.
func (s Store) Set(id, text string) error {
	d, ok := Lookup(id, s.Registered...)
	if !ok {
		return fmt.Errorf("no prompt %q", id)
	}
	path := filepath.Join(s.Dir, d.File)
	if strings.TrimSpace(text) == "" || same(text, d.Default) {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if d.Group == GroupPacks {
		if _, err := packs.FromText(text, strings.TrimPrefix(id, "pack.")); err != nil {
			return fmt.Errorf("%s: %w", d.Label, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, []byte(text), 0o644)
}

// Reset drops the user's version, so the default applies again.
func (s Store) Reset(id string) error { return s.Set(id, "") }

// same is whether two texts differ only in the whitespace at their ends.
func same(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }

// loadDefaults reads the embedded defaults, NN-<id>.md each: frontmatter (group, label, help and a vars block of
// indented `name: what's put there` lines) over the prompt. NN orders the page.
func loadDefaults() ([]Def, error) {
	names, err := fs.Glob(defaultFiles, "defaults/*.md")
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var out []Def
	for _, name := range names {
		b, err := fs.ReadFile(defaultFiles, name)
		if err != nil {
			return nil, err
		}
		d, err := parseDef(idFromFile(name), string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if slices.ContainsFunc(out, func(o Def) bool { return o.ID == d.ID }) {
			return nil, fmt.Errorf("%s: two files claim the id %q", name, d.ID)
		}
		d.File = filepath.Join("prompts", d.ID+".md")
		out = append(out, d)
	}
	return out, nil
}

// idFromFile turns defaults/01-style.code.md into style.code.
func idFromFile(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), ".md")
	if i := strings.Index(base, "-"); i > 0 && strings.Trim(base[:i], "0123456789") == "" {
		return base[i+1:]
	}
	return base
}

func parseDef(id, src string) (Def, error) {
	d := Def{ID: id}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	rest, ok := strings.CutPrefix(src, "---\n")
	if !ok {
		return d, errors.New("no frontmatter: the file must open with a --- line")
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return d, errors.New("the frontmatter is never closed with a --- line")
	}
	inVars := false
	for _, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if inVars && (line[0] == ' ' || line[0] == '\t') {
			name, desc, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				return d, fmt.Errorf("vars: %q is not `name: description`", strings.TrimSpace(line))
			}
			d.Vars = append(d.Vars, Var{Name: name, Desc: strings.TrimSpace(desc)})
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			return d, fmt.Errorf("%q is not `key: value`", line)
		}
		inVars = false
		val = strings.TrimSpace(val)
		switch key {
		case "group":
			d.Group = val
		case "label":
			d.Label = val
		case "help":
			d.Help = val
		case "vars":
			inVars = true
			if val != "" {
				return d, errors.New("vars: put each placeholder on its own indented line")
			}
		default:
			return d, fmt.Errorf("unknown frontmatter key %q", key)
		}
	}
	d.Default = strings.TrimSpace(body)
	if d.Default == "" {
		return d, errors.New("the file has frontmatter but no prompt under it")
	}
	return d, nil
}
