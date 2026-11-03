package claude

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liamg/oriel/agent"
)

// mod is Oriel's Claude Code mod, loaded with --plugin-dir.
//
//go:embed all:mod
var mod embed.FS

var skillName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// writeMod writes the mod into dir, with the pack as its one skill.
func writeMod(dir string, pack agent.Pack) error {
	if pack.Skill != "" && !skillName.MatchString(pack.Skill) {
		return fmt.Errorf("claude: %q is not a skill name: lower-case letters, digits, - and _", pack.Skill)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	err := fs.WalkDir(mod, "mod", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		to := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(p, "mod")))
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := mod.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o644)
	})
	if err != nil || pack.Skill == "" {
		return err
	}
	skill := filepath.Join(dir, "skills", pack.Skill)
	if err := os.MkdirAll(skill, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(skillText(pack)), 0o644)
}

func skillText(p agent.Pack) string {
	name := p.Name
	if name == "" {
		name = p.Skill
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %q\nuser-invocable: true\n---\n\n%s\n",
		p.Skill, "How to run the "+name+" session Oriel started you in.", strings.TrimSpace(p.Text))
}
