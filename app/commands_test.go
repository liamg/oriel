package app_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
)

// commandScene is the whole app, its lazy plugins loaded, and every command it registers.
func commandScene(t *testing.T) (*apptest.Scene, map[string]bool) {
	t.Helper()
	s, _ := apptest.HostScene(t)
	for _, c := range s.Registry().Commands() {
		if strings.HasPrefix(c.ID, "load:") {
			s.Run(c.ID, nil)
		}
	}
	s.Quiet()
	ids := map[string]bool{}
	for _, c := range s.Registry().Commands() {
		ids[c.ID] = true
	}
	return s, ids
}

var helpKeys = regexp.MustCompile(`\{\{keys "([^"]+)"\}\}`)

// Every command something names is one the app registers: a keymap's, a contribution's (a menu item, a start or rail
// entry…), a help page's {{keys}}, and a Run of an id written out in the code. Ids are strings: one renamed and missed
// leaves a key, a button or a help page doing nothing, and nothing else would say so.
func TestNamedCommandsAreRegistered(t *testing.T) {
	s, ids := commandScene(t)
	r := s.Registry()
	known := func(id string) bool { return ids[id] || r.Available(id, nil) }
	missing := map[string][]string{}
	check := func(id, where string) {
		if id != "" && !known(id) {
			missing[id] = append(missing[id], where)
		}
	}

	for _, k := range r.Keymaps() {
		check(k.Command, "keymap "+k.Mode+" "+k.Keys+" ("+k.Source+")")
	}
	for _, kind := range r.ContributionKinds() {
		for _, c := range r.Contributions(kind) {
			v := reflect.Indirect(reflect.ValueOf(c))
			if v.Kind() != reflect.Struct {
				continue
			}
			if f := v.FieldByName("Command"); f.IsValid() && f.Kind() == reflect.String {
				check(f.String(), kind+" "+c.ContributionName())
			}
		}
	}

	pages, _ := filepath.Glob("shell/help/pages/*.md")
	more, _ := filepath.Glob("shell/help/pages/*/*.md")
	for _, p := range append(pages, more...) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range helpKeys.FindAllStringSubmatch(string(b), -1) {
			if !slices.ContainsFunc(sortedKeys(ids), func(id string) bool { ok, _ := path.Match(m[1], id); return ok }) {
				missing[m[1]] = append(missing[m[1]], p+` {{keys "`+m[1]+`"}}: matches no command`)
			}
		}
	}

	for _, lit := range runLiterals(t, "..") {
		check(lit.id, lit.at)
	}

	for _, id := range sortedKeys(missing) {
		t.Errorf("%q is named but no command has it:\n  %s", id, strings.Join(missing[id], "\n  "))
	}
}

type runLiteral struct{ id, at string }

// commandShaped is what a command id looks like: a word, then dotted words; or an ex command's one capitalised word.
var commandShaped = regexp.MustCompile(`^([a-z][a-zA-Z0-9_!-]*(\.[a-zA-Z0-9_!-]+)+|[A-Z][a-zA-Z]+|load:[a-z.]+)$`)

// runLiterals are the command ids written out in the module's code as a Run's: x.Run(ctx, "id", …) and x.run("id", …),
// tests and test harnesses aside.
func runLiterals(t *testing.T, root string) []runLiteral {
	t.Helper()
	var out []runLiteral
	fset := token.NewFileSet()
	for _, dir := range []string{"app", "host", "ui", "plugin", "script", "registry", "agent", "assistant", "catalog"} {
		filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() && (d.Name() == "testdata" || d.Name() == "apptest") {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Run" && sel.Sel.Name != "run" {
					return true
				}
				for i, a := range call.Args {
					if i > 1 {
						break
					}
					lit, ok := a.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if id, err := strconv.Unquote(lit.Value); err == nil && commandShaped.MatchString(id) {
						out = append(out, runLiteral{id, fset.Position(lit.Pos()).String()})
					}
				}
				return true
			})
			return nil
		})
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// commandVerbs are the words a command's id ends in: what it does.
var commandVerbs = []string{
	"open", "close", "toggle",
	"new", "add", "remove", "delete", "rename", "duplicate", "edit", "set", "reset", "save", "clear", "undo", "redo",
	"run", "start", "stop", "restart", "refresh", "reload", "load", "check", "install", "uninstall", "update", "enable", "disable",
	"copy", "cut", "paste", "send", "apply", "discard", "keep", "pick", "select", "focus", "move", "dismiss", "drag",
	"next", "prev", "back", "forward", "up", "down", "in", "out", "into", "over",
	"confirm", "cancel", "accept",
	"fetch", "push", "merge", "commit", "stage", "unstage", "sync", "raise", "ready", "redraft", "adopt", "clone", "scan", "tag",
	"ask", "answer", "say", "explain", "comment", "review", "resolve", "reopen", "fold", "expand", "narrow", "widen", "pin",
	"filter", "browse", "search", "preview", "reveal", "zoom", "equalize", "launch", "resume", "end", "archive", "interrupt",
	"attach", "anchor", "compose", "enter", "drill", "split", "speak", "download", "eval", "watch", "continue", "pause",
	"step", "trust", "consent", "fix", "rework", "submit", "recheck", "include", "summarize", "housekeep", "follow",
}

// commandShape is a command id as the convention writes it: lower-case dotted words, a word's parts joined by _.
var commandShape = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// conventional says id is named as the convention says: noun first, then its parts, then a verb from commandVerbs.
func conventional(id string) bool {
	if !commandShape.MatchString(id) {
		return false
	}
	return slices.Contains(commandVerbs, id[strings.LastIndex(id, ".")+1:])
}

// commandNamesCeiling is how many built-in commands aren't yet named as the convention says (docs/architecture.md,
// "Command names"). It may only fall: lower it as commands are renamed.
const commandNamesCeiling = 177

// Built-in commands are named by one convention, so a command reads the same everywhere and a new one is named like
// the rest: lower-case dotted words, the thing first and what's done to it last, from a fixed set of verbs
// (term.host.open, not term.host). An ex command's capitalised name (:Settings) is its alias, not its id. The ones
// named before the convention count against a ceiling that only falls.
func TestCommandNamesFollowTheConvention(t *testing.T) {
	s, _ := commandScene(t)
	var off []string
	for _, c := range s.Registry().Commands() {
		if !strings.HasPrefix(c.Source, "builtin") || strings.HasPrefix(c.ID, "load:") || conventional(c.ID) {
			continue
		}
		off = append(off, c.ID)
	}
	switch {
	case len(off) > commandNamesCeiling:
		t.Errorf("%d built-in commands aren't named by the convention, over its ceiling of %d. Name a new one as "+
			"noun[.noun].verb, lower-case, its verb from commandVerbs. Those off it:\n  %s", len(off), commandNamesCeiling, strings.Join(off, "\n  "))
	case len(off) < commandNamesCeiling:
		t.Errorf("only %d built-in commands are off the convention now: lower commandNamesCeiling to %d", len(off), len(off))
	}
}
