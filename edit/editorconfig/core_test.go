package editorconfig

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// ecTest is one test of the vendored core-test suite (spec/editorconfig-core-test): run the core on a file with
// an EditorConfig file name, and match what it prints, key=value per line, against a regular expression.
type ecTest struct {
	dir, name, ecFile, src, regex, version string
	sorted                                 bool // the _multiline kind: lines sorted, then matched whole
}

// cmakeString undoes CMake's quoting and escapes, with ${CMAKE_CURRENT_SOURCE_DIR} as dir.
func cmakeString(s, dir string) string {
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) && len(s) >= 2 {
		s = s[1 : len(s)-1]
	}
	s = strings.ReplaceAll(s, "${CMAKE_CURRENT_SOURCE_DIR}", dir)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// args splits a CMake call's arguments: words, or quoted strings.
func args(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r':
			i++
		case s[i] == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' {
					j++
				}
				j++
			}
			out = append(out, s[i:j+1])
			i = j + 1
		default:
			j := i
			for j < len(s) && !strings.ContainsRune(" \t\n\r", rune(s[j])) {
				j++
			}
			out = append(out, s[i:j])
			i = j
		}
	}
	return out
}

var setRe = regexp.MustCompile(`(?m)^\s*set\((\w+)\s+(".*")\)\s*$`)

// cmakeRegex is a CMake regular expression in Go's syntax: a backslash in brackets is literal there.
func cmakeRegex(re string) string {
	var b strings.Builder
	in := false
	for i := 0; i < len(re); i++ {
		c := re[i]
		switch {
		case c == '[' && !in:
			in = true
		case c == ']' && in && !strings.HasSuffix(b.String(), "["):
			in = false
		case c == '\\' && in:
			b.WriteString(`\\`)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

var callRe = regexp.MustCompile(`(?ms)^(new_ec_test(?:_multiline|_version|_full_ec_file_path)?)\((.*?)\)\s*$`)

func loadSuite(t *testing.T) []ecTest {
	t.Helper()
	root := filepath.Join("..", "..", "spec", "editorconfig-core-test")
	var out []ecTest
	for _, sub := range []string{"glob", "properties", "parser", "filetree"} {
		dir, _ := filepath.Abs(filepath.Join(root, sub))
		b, err := os.ReadFile(filepath.Join(dir, "CMakeLists.txt"))
		if err != nil {
			t.Fatal(err)
		}
		var lines []string
		for _, l := range strings.Split(string(b), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(l), "#") {
				lines = append(lines, l)
			}
		}
		src := strings.Join(lines, "\n")
		vars := map[string]string{}
		for _, m := range setRe.FindAllStringSubmatch(src, -1) {
			vars[m[1]] = m[2]
		}
		for _, m := range callRe.FindAllStringSubmatch(src, -1) {
			a := args(m[2])
			for i, x := range a {
				if strings.HasPrefix(x, "${") && strings.HasSuffix(x, "}") {
					if v, ok := vars[x[2:len(x)-1]]; ok {
						a[i] = v
					}
				}
			}
			tt := ecTest{dir: dir, name: a[0], ecFile: cmakeString(a[1], dir), src: cmakeString(a[2], dir), regex: cmakeString(a[3], dir)}
			switch m[1] {
			case "new_ec_test_multiline":
				tt.sorted = true
			case "new_ec_test_version":
				tt.version = cmakeString(a[4], dir)
			}
			out = append(out, tt)
		}
	}
	return out
}

// The EditorConfig core test suite: every glob, property, parser and file-tree test passes.
// ref:   editorconfig/index.rst#Terminology
// quote: "A conforming core or plugin must pass the tests in the core-tests repository"
func TestCoreSuite(t *testing.T) {
	tests := loadSuite(t)
	if len(tests) < 150 {
		t.Fatalf("only %d tests in the suite", len(tests))
	}
	for _, tt := range tests {
		path := tt.src
		if !filepath.IsAbs(path) {
			path = filepath.Join(tt.dir, path)
		}
		res, err := Resolve(path, Options{Name: tt.ecFile, Version: tt.version})
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		var lines []string
		for _, p := range res.Pairs {
			lines = append(lines, p.Key+"="+p.Value)
		}
		re := cmakeRegex(tt.regex)
		if tt.sorted {
			slices.Sort(lines)
			re = "^[\r\n]*" + re + "$"
		}
		out := strings.Join(lines, "\n")
		if len(lines) > 0 {
			out += "\n"
		}
		ok, err := regexp.MatchString(re, out)
		if err != nil {
			t.Errorf("%s: regex %q: %v", tt.name, re, err)
			continue
		}
		if !ok {
			t.Errorf("%s (%s, %s): got %q, want /%s/", tt.name, filepath.Base(tt.dir), tt.src, out, re)
		}
	}
}

// A pair knows its file and line, for the settings page; a line that's none of the line types is a problem with its
// line, and the file's other sections still apply.
func TestSourcesAndProblems(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, ".editorconfig"), []byte("root = true\n\n[*]\nindent_style = space\nindent_size = 4\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", ".editorconfig"), []byte("[*.go]\nnonsense line\nindent_style = tab\n"), 0o644)
	res, err := Resolve(filepath.Join(dir, "sub", "a.go"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Pair{}
	for _, p := range res.Pairs {
		got[p.Key] = p
	}
	if p := got["indent_style"]; p.Value != "tab" || p.File != filepath.Join(dir, "sub", ".editorconfig") || p.Line != 3 {
		t.Errorf("indent_style: %+v", p)
	}
	if p := got["indent_size"]; p.Value != "4" || p.Line != 5 {
		t.Errorf("indent_size: %+v", p)
	}
	// ref:   editorconfig/index.rst#File Format
	// quote: "Any line that is not one of the above is invalid."
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0].Error(), ".editorconfig:2") {
		t.Errorf("problems: %v", res.Problems)
	}
	if len(res.Looked) != 2 {
		t.Errorf("looked at %v; root = true stops the search", res.Looked)
	}
}
