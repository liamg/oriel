package cli

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// oriel plugin pack builds the release archives and prints their sha256s; oriel plugin init starts a plugin that
// pack takes as it is.
func TestPluginPackAndInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "oriel-greet")
	var out, errs bytes.Buffer
	if code := run([]string{"plugin", "init", "--id", "acme/greet", dir}, &out, &errs); code != 0 {
		t.Fatalf("init: %d %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "oriel.toml") || !strings.Contains(out.String(), "init.lua") {
		t.Errorf("init wrote %q", out.String())
	}
	out.Reset()
	if code := run([]string{"plugin", "pack", dir}, &out, &errs); code != 0 {
		t.Fatalf("pack: %d %s", code, errs.String())
	}
	if !regexp.MustCompile(`(?m)^sha256:[0-9a-f]{64}  any  dist/greet-0\.1\.0\.tar\.gz$`).MatchString(out.String()) {
		t.Errorf("pack printed %q", out.String())
	}
	if code := run([]string{"plugin", "init", "--id", "acme/greet", dir}, &out, &errs); code == 0 ||
		!strings.Contains(errs.String(), "is there already") {
		t.Errorf("init over a plugin: %d %q", code, errs.String())
	}
}
