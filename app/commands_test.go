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
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
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

	pages, _ := filepath.Glob("help/pages/*.md")
	more, _ := filepath.Glob("help/pages/*/*.md")
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
		if err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
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
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
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
	"debug", "complete", "scroll", "rebuild", "quit",
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

// Built-in commands are named by one convention, so a command reads the same everywhere and a new one is named like
// the rest: lower-case dotted words, the thing first and what's done to it last, from a fixed set of verbs
// (term.host.open, not term.host). An ex command's name (:Settings, :term) is its Ex, an alias, not its id.
func TestCommandNamesFollowTheConvention(t *testing.T) {
	s, _ := commandScene(t)
	var off []string
	for _, c := range s.Registry().Commands() {
		if !strings.HasPrefix(c.Source, "builtin") || strings.HasPrefix(c.ID, "load:") || conventional(c.ID) {
			continue
		}
		off = append(off, c.ID)
	}
	if len(off) > 0 {
		t.Errorf("%d built-in commands aren't named by the convention. Name one as noun[.noun].verb, lower-case, its "+
			"verb from commandVerbs, and give it an Ex for its ex name. Those off it:\n  %s", len(off), strings.Join(off, "\n  "))
	}
}

// Every built-in command says what a plugin needs to run it with oriel.run: a capability it must hold, Open on
// purpose, or UserOnly. A new command that acts has to choose, so none is left open by forgetting.
func TestBuiltinCommandsDeclareTheirGrant(t *testing.T) {
	s, _ := commandScene(t)
	var none, unknown []string
	for _, c := range s.Registry().Commands() {
		if !strings.HasPrefix(c.Source, "builtin") || strings.HasPrefix(c.ID, "load:") {
			continue
		}
		switch _, known := plugin.Capability(c.Grant).Info(); {
		case c.UserOnly:
		case c.Grant == "":
			none = append(none, c.ID)
		case c.Grant != registry.Open && !known:
			unknown = append(unknown, c.ID+" ("+c.Grant+")")
		}
	}
	if len(none) > 0 {
		t.Errorf("%d built-in commands say nothing of who may run them. Give each a Grant: the capability a plugin "+
			"needs (string(plugin.CapExec)…), registry.Open when any plugin may, or UserOnly:\n  %s", len(none), strings.Join(none, "\n  "))
	}
	if len(unknown) > 0 {
		t.Errorf("built-in commands name a Grant that isn't a capability:\n  %s", strings.Join(unknown, "\n  "))
	}
}

// Every built-in command declares the arguments it takes, and what runs one (a keymap, a contribution such as a menu
// item or a rail entry, a Run written out in the code) passes only those, of their types: a misspelled key would
// otherwise read as nothing.
func TestBuiltinCommandsDeclareTheirArgs(t *testing.T) {
	s, _ := commandScene(t)
	r := s.Registry()
	var none []string
	for _, c := range r.Commands() {
		if strings.HasPrefix(c.Source, "builtin") && !strings.HasPrefix(c.ID, "load:") && c.Args == nil {
			none = append(none, c.ID)
		}
	}
	if len(none) > 0 {
		t.Errorf("%d built-in commands don't say what arguments they take. Give each Args (registry.NoArgs for none):\n  %s",
			len(none), strings.Join(none, "\n  "))
	}
	check := func(id string, args registry.Args, where string) {
		c, ok := r.CommandFor(id)
		if !ok || len(args) == 0 {
			return
		}
		known := registry.Args{}
		for k, v := range args {
			if _, unknown := v.(present); !unknown {
				known[k] = v
			} else if !c.Takes(k) {
				t.Errorf("%s: command %q doesn't take %q", where, id, k)
			}
		}
		if err := registry.CheckArgs(c, known); err != nil && !strings.Contains(err.Error(), "is required") {
			t.Errorf("%s: %v", where, err)
		}
	}
	for _, k := range r.Keymaps() {
		check(k.Command, k.Args, "keymap "+k.Mode+" "+k.Keys+" ("+k.Source+")")
	}
	for _, kind := range r.ContributionKinds() {
		for _, c := range r.Contributions(kind) {
			v := reflect.Indirect(reflect.ValueOf(c))
			if v.Kind() != reflect.Struct {
				continue
			}
			cmd, args := v.FieldByName("Command"), v.FieldByName("Args")
			if !cmd.IsValid() || cmd.Kind() != reflect.String || !args.IsValid() {
				continue
			}
			if a, ok := args.Interface().(registry.Args); ok {
				check(cmd.String(), a, kind+" "+c.ContributionName())
			}
		}
	}
	for _, lit := range runArgLiterals(t, "..") {
		check(lit.id, lit.args, lit.at)
	}
}

type runArgLiteral struct {
	id, at string
	args   registry.Args
}

// runArgLiterals are the Runs written out in the code with their args as a literal: x.Run(ctx, "id",
// registry.Args{"k": v}) and kit.Call{Command: "id", Args: registry.Args{…}}. Only the keys are known, so each value
// is any.
func runArgLiterals(t *testing.T, root string) []runArgLiteral {
	t.Helper()
	var out []runArgLiteral
	fset := token.NewFileSet()
	keysOf := func(e ast.Expr) (registry.Args, bool) {
		cl, ok := e.(*ast.CompositeLit)
		if !ok {
			return nil, false
		}
		if sel, ok := cl.Type.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Args" {
			return nil, false
		}
		a := registry.Args{}
		for _, el := range cl.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				return nil, false
			}
			lit, ok := kv.Key.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return nil, false
			}
			k, _ := strconv.Unquote(lit.Value)
			a[k] = argValue(kv.Value)
		}
		return a, true
	}
	for _, dir := range []string{"app", "host", "ui", "plugin", "script", "registry", "agent", "catalog"} {
		if err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
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
				switch v := n.(type) {
				case *ast.CallExpr:
					sel, ok := v.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Run" && sel.Sel.Name != "run" || len(v.Args) < 2 {
						return true
					}
					for i := 0; i+1 < len(v.Args) && i < 2; i++ {
						lit, ok := v.Args[i].(*ast.BasicLit)
						if !ok || lit.Kind != token.STRING {
							continue
						}
						id, err := strconv.Unquote(lit.Value)
						if err != nil || !commandShaped.MatchString(id) {
							continue
						}
						if a, ok := keysOf(v.Args[i+1]); ok {
							out = append(out, runArgLiteral{id, fset.Position(lit.Pos()).String(), a})
						}
					}
				case *ast.CompositeLit:
					var id string
					var args ast.Expr
					for _, el := range v.Elts {
						kv, ok := el.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						k, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						switch k.Name {
						case "Command":
							if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
								id, _ = strconv.Unquote(lit.Value)
							}
						case "Args":
							args = kv.Value
						}
					}
					if id != "" && args != nil {
						if a, ok := keysOf(args); ok {
							out = append(out, runArgLiteral{id, fset.Position(v.Pos()).String(), a})
						}
					}
				}
				return true
			})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// argValue is a literal arg's value where it's plain (a string, a number, a bool), else nil: present, its type unknown.
func argValue(e ast.Expr) any {
	switch v := e.(type) {
	case *ast.BasicLit:
		switch v.Kind {
		case token.STRING:
			s, _ := strconv.Unquote(v.Value)
			return s
		case token.INT:
			n, _ := strconv.Atoi(v.Value)
			return n
		case token.FLOAT:
			f, _ := strconv.ParseFloat(v.Value, 64)
			return f
		}
	case *ast.Ident:
		switch v.Name {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return present{}
}

// present is a literal arg whose value's type isn't known from its source: the key is there, its type unchecked.
type present struct{}

// A built-in right-click menu item is named like the command it mirrors: noun[.noun].verb, lower-case dotted words,
// a word's parts joined by _ (explorer.relative_path.copy, tab.others.close).
func TestMenuItemNamesFollowTheConvention(t *testing.T) {
	s, _ := commandScene(t)
	var off []string
	n := 0
	for _, m := range registry.ContributionsOf[registry.MenuItem](s.Registry()) {
		n++
		if !conventional(m.Name) {
			off = append(off, m.Name)
		}
	}
	if n < 10 {
		t.Fatalf("found %d menu items: the list missed them", n)
	}
	if len(off) > 0 {
		t.Errorf("%d menu items aren't named as noun[.noun].verb:\n  %s", len(off), strings.Join(off, "\n  "))
	}
}
