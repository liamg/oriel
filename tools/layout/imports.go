package layout

import (
	"bufio"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const modulePrefix = "github.com/liamg/oriel/"

// ImportRule says module From never imports any module in Never.
type ImportRule struct {
	From  string
	Never []string
}

// ImportRules reads architecture.md's "`a`, `b` never import `x` or `y`." lines.
func ImportRules(root string) ([]ImportRule, error) {
	f, err := os.Open(filepath.Join(root, "_design", "architecture.md"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []ImportRule
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		from, never, ok := strings.Cut(sc.Text(), " never import ")
		if !ok || !strings.HasPrefix(from, "- ") {
			continue
		}
		var targets []string
		for _, m := range codeSpan.FindAllStringSubmatch(never, -1) {
			targets = append(targets, m[1])
		}
		for _, m := range codeSpan.FindAllStringSubmatch(from, -1) {
			out = append(out, ImportRule{From: m[1], Never: targets})
		}
	}
	return out, sc.Err()
}

type Violation struct {
	File   string // relative to the root
	Import string
	Rule   ImportRule
	Target string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s imports %s, but %s never imports %s", v.File, v.Import, v.Rule.From, v.Target)
}

// Violations parses every Go file under each rule's module, tests included, and reports forbidden imports.
func Violations(root string, rules []ImportRule) ([]Violation, error) {
	var out []Violation
	for _, rule := range rules {
		dir := filepath.Join(root, rule.From)
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != dir && (d.Name() == "vendor" || d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, imp := range f.Imports {
				p, _ := strconv.Unquote(imp.Path.Value)
				for _, t := range rule.Never {
					if p == modulePrefix+t || strings.HasPrefix(p, modulePrefix+t+"/") {
						out = append(out, Violation{File: filepath.ToSlash(rel), Import: p, Rule: rule, Target: t})
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
