package gh

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Template is a checkout's pull request template and where it was found ("" for none), where GitHub looks: .github,
// the root and docs, any case; then the first of .github/PULL_REQUEST_TEMPLATE/.
func Template(dir string) (body, file string) {
	for _, sub := range []string{".github", "", "docs"} {
		if f := findFold(filepath.Join(dir, sub), "pull_request_template.md"); f != "" {
			if b, err := os.ReadFile(filepath.Join(dir, sub, f)); err == nil {
				return string(b), path.Join(sub, f)
			}
		}
	}
	tdir := findFold(filepath.Join(dir, ".github"), "pull_request_template")
	if tdir == "" {
		return "", ""
	}
	es, _ := os.ReadDir(filepath.Join(dir, ".github", tdir))
	for _, e := range es {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".md") {
			if b, err := os.ReadFile(filepath.Join(dir, ".github", tdir, e.Name())); err == nil {
				return string(b), path.Join(".github", tdir, e.Name())
			}
		}
	}
	return "", ""
}

// findFold is the name in dir that is name in any case, or "".
func findFold(dir, name string) string {
	es, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range es {
		if strings.EqualFold(e.Name(), name) {
			return e.Name()
		}
	}
	return ""
}

// ownerRule is a CODEOWNERS line.
type ownerRule struct {
	re     *regexp.Regexp
	owners []string
}

// Owners are who CODEOWNERS (in .github, the root or docs) says owns files, each named once, in the order their
// rules name them: the reviewers GitHub would ask. A file's last matching rule wins, as GitHub reads it.
func Owners(dir string, files []string) []string {
	var rules []ownerRule
	for _, sub := range []string{".github", "", "docs"} {
		f, err := os.Open(filepath.Join(dir, sub, "CODEOWNERS"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if i := strings.Index(line, "#"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			fs := strings.Fields(line)
			if len(fs) < 1 {
				continue
			}
			rules = append(rules, ownerRule{re: ownerPattern(fs[0]), owners: fs[1:]})
		}
		f.Close()
		break
	}
	var out []string
	for _, file := range files {
		file = strings.TrimPrefix(filepath.ToSlash(file), "/")
		for i := len(rules) - 1; i >= 0; i-- {
			if rules[i].re.MatchString(file) {
				for _, o := range rules[i].owners {
					if o = strings.TrimPrefix(o, "@"); !slices.Contains(out, o) {
						out = append(out, o)
					}
				}
				break
			}
		}
	}
	return out
}

// ownerPattern turns a CODEOWNERS pattern (gitignore's rules) into a regexp over a path from the root.
func ownerPattern(p string) *regexp.Regexp {
	anchored := strings.HasPrefix(p, "/") || strings.Contains(strings.TrimSuffix(p, "/"), "/")
	dir := strings.HasSuffix(p, "/")
	p = strings.Trim(p, "/")
	var b strings.Builder
	if anchored {
		b.WriteString("^")
	} else {
		b.WriteString("(^|/)")
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; {
		case strings.HasPrefix(p[i:], "**/"):
			b.WriteString("(.*/)?")
			i += 2
		case strings.HasPrefix(p[i:], "**"):
			b.WriteString(".*")
			i++
		case c == '*':
			b.WriteString("[^/]*")
		case c == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	if dir {
		b.WriteString("/")
	} else {
		b.WriteString("(/|$)")
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return regexp.MustCompile(`$^`)
	}
	return re
}
