package api

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// diagHost keeps the diagnostics and coverage set on it, by owner and file.
type diagHost struct {
	plugin.Host
	diags map[[2]string][]registry.Diagnostic
	cover map[[2]string]map[int]int
}

func (h *diagHost) SetDiagnostics(owner, file string, ds []registry.Diagnostic) {
	if ds == nil {
		delete(h.diags, [2]string{owner, file})
		return
	}
	h.diags[[2]string{owner, file}] = ds
}

func (h *diagHost) SetCoverage(owner, file string, hits map[int]int) {
	if hits == nil {
		delete(h.cover, [2]string{owner, file})
		return
	}
	h.cover[[2]string{owner, file}] = hits
}

func diagAPIs() (*APIs, *diagHost) {
	h := &diagHost{diags: map[[2]string][]registry.Diagnostic{}, cover: map[[2]string]map[int]int{}}
	return &APIs{Host: h, Plugin: plugin.Caller{ID: "acme/lint"}}, h
}

// A plugin's diagnostics and coverage are its own: under an owner its id scopes, so they can't replace or clear a
// built-in's of the same name (lua, tests, an LSP server's).
func TestAPluginsDiagnosticsAreItsOwn(t *testing.T) {
	a, h := diagAPIs()
	h.diags[[2]string{"gopls", "/p/a.go"}] = []registry.Diagnostic{{Message: "the server's"}}
	h.cover[[2]string{"tests", "/p/a.go"}] = map[int]int{0: 1}
	if err := (Diagnostics{a}).Set("gopls", "/p/a.go", []any{script.Table{"line": 1.0, "message": "the plugin's"}}); err != nil {
		t.Fatal(err)
	}
	if err := (Coverage{a}).Set("tests", "/p/a.go", script.Table{"1": 0.0}); err != nil {
		t.Fatal(err)
	}
	if ds := h.diags[[2]string{"gopls", "/p/a.go"}]; len(ds) != 1 || ds[0].Message != "the server's" {
		t.Errorf("the server's diagnostics: %+v", ds)
	}
	if ds := h.diags[[2]string{"acme/lint:gopls", "/p/a.go"}]; len(ds) != 1 || ds[0].Message != "the plugin's" {
		t.Errorf("the plugin's: %+v", h.diags)
	}
	if c := h.cover[[2]string{"tests", "/p/a.go"}]; c[0] != 1 {
		t.Errorf("the tests' coverage: %v", h.cover)
	}
	a.Dispose()
	if len(h.diags) != 1 || len(h.cover) != 1 {
		t.Errorf("unloaded, the plugin cleared others' too: %v %v", h.diags, h.cover)
	}
}

// Setting a file's diagnostics again, as a linter does on every save, keeps one way to clear them, not one per call.
func TestSettingDiagnosticsAgainDoesntGrowWhatsOwned(t *testing.T) {
	a, _ := diagAPIs()
	for range 50 {
		if err := (Diagnostics{a}).Set("lint", "/p/a.go", []any{}); err != nil {
			t.Fatal(err)
		}
		if err := (Coverage{a}).Set("lint", "/p/a.go", script.Table{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := a.Registered(); n != 2 {
		t.Errorf("%d owned after 100 sets of one file, want 2", n)
	}
}

// Coverage that isn't a table is a mistake in the call: it raises, and leaves what was there.
func TestCoverageOfTheWrongTypeRaises(t *testing.T) {
	a, h := diagAPIs()
	if err := (Coverage{a}).Set("lint", "/p/a.go", script.Table{"1": 2.0}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []any{"all of it", 7.0, true} {
		if err := (Coverage{a}).Set("lint", "/p/a.go", bad); err == nil || !strings.Contains(err.Error(), "coverage is") {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if c := h.cover[[2]string{"acme/lint:lint", "/p/a.go"}]; c[0] != 2 {
		t.Errorf("a bad call changed it: %v", h.cover)
	}
}
