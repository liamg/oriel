package typeset

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"sync"

	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

// fontRef is one face in a font file: a collection holds several.
type fontRef struct {
	file  string
	index int
}

// families are the installed font families: each one's faces, by its name.
type families struct {
	once sync.Once
	dirs []string // where they're installed; the system's font folders if nil
	by   map[string][]fontRef
}

// scan reads the name of every installed face once; a file that can't be read is passed over.
func (fs *families) scan() map[string][]fontRef {
	fs.once.Do(func() {
		fs.by = map[string][]fontRef{}
		files := systemFontFiles()
		if fs.dirs != nil {
			files = fontFilesIn(fs.dirs)
		}
		for _, file := range files {
			data, unmap, err := mapFile(file)
			if err != nil {
				continue
			}
			lds, err := ot.NewLoaders(bytes.NewReader(data))
			if err == nil {
				for i, ld := range lds {
					if d, _ := gtfont.Describe(ld, nil); d.Family != "" && !strings.HasPrefix(d.Family, ".") {
						fs.by[d.Family] = append(fs.by[d.Family], fontRef{file, i})
					}
				}
			}
			unmap()
		}
	})
	return fs.by
}

// SetFontDirs has Families and Use find installed fonts in dirs alone, not the system's folders: a test's own.
// It's for before either is first called.
func (t *Typesetter) SetFontDirs(dirs []string) { t.installed.dirs = dirs }

// Families are the font families installed on this machine, sorted: what Use takes. The first call reads every
// font file's names, so it's for off the frame path.
func (t *Typesetter) Families() []string {
	by := t.installed.scan()
	out := make([]string, 0, len(by))
	for f := range by {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	return out
}

// Use draws role (UI, Mono or Display) with the installed family's faces from now on; "" goes back to the bundled
// face. A rune the family lacks still comes from the bundled faces, then the system's. It loads the family's files,
// so it's for off the frame path.
func (t *Typesetter) Use(role, family string) error {
	var faces []*Face
	if family != "" {
		refs, ok := t.installed.scan()[family]
		if !ok {
			return fmt.Errorf("no font family %q is installed", family)
		}
		for _, r := range refs {
			if fc := loadIndex(r); fc != nil {
				faces = append(faces, fc)
			}
		}
		if len(faces) == 0 {
			return fmt.Errorf("the %s font's files can't be read", family)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.used == nil {
		t.used = map[string][]*Face{}
	}
	if family == "" {
		delete(t.used, role)
	} else {
		t.used[role] = faces
	}
	t.runs, t.old = map[runKey]*Run{}, nil
	t.gen++
	return nil
}

// Gen moves on each time the faces a role draws with change: what's measured with the old ones is stale.
func (t *Typesetter) Gen() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.gen
}

// loadIndex parses face r.index of r.file.
func loadIndex(r fontRef) *Face {
	data, unmap, err := mapFile(r.file)
	if err != nil {
		return nil
	}
	defer unmap()
	lds, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil || r.index >= len(lds) {
		return nil
	}
	fnt, err := gtfont.NewFont(lds[r.index]) // go-text copies what it reads: nothing refers to data after this
	if err != nil {
		return nil
	}
	d, _ := gtfont.Describe(lds[r.index], nil)
	return newFace(fnt, fmt.Sprintf("%s#%d", r.file, r.index), d)
}

// face is the face f draws with: the family a role is set to use, if it has one, else the bundled one. t.mu is held.
func (t *Typesetter) face(f FontSpec) *Face {
	role := f.Family
	if role == "" || match(t.bundled, f) != nil && match(t.bundled, f).Family != f.Family {
		role = UI // a family the bundled faces haven't (none said) is drawn as the UI's
	}
	if faces := t.used[role]; len(faces) > 0 {
		g := f
		g.Family = faces[0].Family
		if fc := match(faces, g); fc != nil {
			return fc
		}
	}
	return match(t.bundled, f)
}
