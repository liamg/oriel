package layout

import (
	"os"
	"slices"
	"testing"
)

func root(t *testing.T) string {
	wd, _ := os.Getwd()
	r, err := Root(wd)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Everything that ships is one module; spec/ holds reference sources kept out of the build, and third_party/ the
// dependencies go.mod replaces with a patched copy (each says why in its PATCHED.md).
func TestOneModule(t *testing.T) {
	dirs, err := ModuleDirs(root(t))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".", "spec", "third_party/wazero"}; !slices.Equal(dirs, want) {
		t.Errorf("go.mod files in %q, want %q", dirs, want)
	}
}

func TestLayering(t *testing.T) {
	pkgs, err := List(root(t))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, v := range Check(pkgs, Rules) {
		seen[v.Key()] = true
		if !slices.Contains(Known, v.Key()) {
			t.Error(v)
		}
	}
	for _, k := range Known {
		if !seen[k] {
			t.Errorf("%s no longer happens: take it off Known", k)
		}
	}
}

func TestCheckIsTransitive(t *testing.T) {
	m := func(p string) string { return Module + "/" + p }
	pkgs := []Package{
		{ImportPath: m("vim"), Deps: []string{m("registry"), m("ui/widget"), "fmt"}},
		{ImportPath: m("vim/vimtest"), TestImports: []string{m("app/harness")}},
		{ImportPath: m("app/harness"), Deps: []string{m("render")}},
		{ImportPath: m("core"), Deps: []string{m("text")}},
		{ImportPath: m("uikit"), Deps: []string{m("ui")}},
	}
	got := Check(pkgs, []Rule{{From: []string{"vim", "core"}, Never: []string{"ui", "render"}}})
	var s []string
	for _, v := range got {
		s = append(s, v.String())
	}
	want := []string{
		m("vim") + " reaches " + m("ui/widget") + ", but vim never reaches ui",
		m("vim/vimtest") + " reaches " + m("render") + ", but vim never reaches render",
	}
	if !slices.Equal(s, want) {
		t.Errorf("violations:\n%q\nwant:\n%q", s, want)
	}
}

// A rule's Except are packages under From it leaves to rules of their own.
func TestCheckExcept(t *testing.T) {
	m := func(p string) string { return Module + "/" + p }
	pkgs := []Package{
		{ImportPath: m("registry"), Deps: []string{m("core")}},
		{ImportPath: m("registry/keymap"), TestImports: []string{m("ui")}},
		{ImportPath: m("registry/uireg"), Deps: []string{m("ui"), m("lsp")}},
		{ImportPath: m("registry/uireg/x"), Deps: []string{m("render")}},
	}
	rules := []Rule{
		{From: []string{"registry"}, Never: []string{"ui", "render", "lsp"}, Except: []string{"registry/uireg"}},
		{From: []string{"registry/uireg"}, Never: []string{"lsp"}},
	}
	var got []string
	for _, v := range Check(pkgs, rules) {
		got = append(got, v.Package+" -> "+v.Via)
	}
	want := []string{m("registry/keymap") + " -> " + m("ui"), m("registry/uireg") + " -> " + m("lsp")}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// A feature package never imports another feature, nor the app that lists them.
func TestFeaturesStandAlone(t *testing.T) {
	pkgs, err := List(root(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range FeatureImports(pkgs) {
		t.Error(v)
	}
}

func TestFeatureImports(t *testing.T) {
	m := func(p string) string { return Module + "/" + p }
	pkgs := []Package{
		{ImportPath: m("app"), Imports: []string{m("app/stats"), m("app/kit")}},
		{ImportPath: m("app/stats"), Imports: []string{m("app/kit"), m("registry")}, XTestImports: []string{m("app/apptest"), m("app/stats")}},
		{ImportPath: m("app/explain"), Imports: []string{m("app/stats")}},
		{ImportPath: m("app/settings/lua"), TestImports: []string{m("app")}},
		{ImportPath: m("app/apptest"), Imports: []string{m("app")}},
	}
	var got []string
	for _, v := range FeatureImports(pkgs) {
		got = append(got, v.Package+" -> "+v.Via)
	}
	want := []string{m("app/explain") + " -> " + m("app/stats"), m("app/settings/lua") + " -> " + m("app")}
	if !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
