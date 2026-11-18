package app_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
)

var kebab = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// A built-in plugin's id is kebab-case, lower-case words joined by "-" (docs/architecture.md, "Plugin ids"), as a
// third-party manifest's <owner>/<name> parts are: never dotted, which reads as a command, nor run together. Every
// built-in the window starts with is checked, those waiting for their first use too.
func TestBuiltinPluginIDsAreKebabCase(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	bs, ok := kit.BuiltinsOf(s.Registry())
	if !ok {
		t.Fatal("no built-ins service")
	}
	n := 0
	for _, b := range bs.Builtins() {
		if b.Manifest.ID == "" {
			continue
		}
		n++
		if !kebab.MatchString(b.Manifest.ID) {
			t.Errorf("plugin id %q isn't kebab-case", b.Manifest.ID)
		}
	}
	if n < 40 {
		t.Fatalf("found %d built-in plugins: the list missed them", n)
	}
}

// A view id is kebab-case; one that fills a slot is the slot's name, a dot, then its own kebab-case name
// (docs/architecture.md, "View ids"). No view id is a command id. Every built-in's views are checked, the lazy ones
// loaded first.
func TestBuiltinViewIDsFollowTheConvention(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	bs, _ := kit.BuiltinsOf(s.Registry())
	for _, b := range bs.Builtins() {
		if b.Waits {
			s.Run(registry.LoadCommand(b.Manifest.ID), nil)
		}
	}
	commands := map[string]bool{}
	for _, c := range s.Registry().Commands() {
		commands[c.ID] = true
	}
	views := s.Host().Views()
	if len(views) < 40 {
		t.Fatalf("found %d views: the list missed them", len(views))
	}
	for _, id := range views {
		if commands[id] {
			t.Errorf("view %q is also a command's id", id)
		}
		name := id
		if slot := viewSlot(id); slot != "" {
			name = strings.TrimPrefix(id, slot+".")
		}
		if !kebab.MatchString(name) {
			t.Errorf("view id %q isn't a kebab-case name, nor a slot's name then one", id)
		}
	}
}

// viewSlot is the longest slot id's name starts with, or "".
func viewSlot(id string) string {
	slots := slices.Clone(kit.ViewSlots)
	slices.SortFunc(slots, func(a, b string) int { return len(b) - len(a) })
	for _, s := range slots {
		if strings.HasPrefix(id, s+".") {
			return s
		}
	}
	return ""
}
