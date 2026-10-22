package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// [contributes] lists themes, fonts, icons, languages, prompts and help, as globs of the plugin's own files.
func TestManifestContributes(t *testing.T) {
	m, err := ParseManifest([]byte(`id = "acme/dusk"
version = "1.0.0"
[contributes]
themes    = ["assets/themes/*.toml"]
fonts     = ["assets/fonts/*.ttf"]
icons     = "assets/icons/"
languages = ["assets/languages/*"]
prompts   = ["assets/prompts/*.md"]
help      = ["assets/help/*.md"]
`))
	if err != nil {
		t.Fatal(err)
	}
	c := m.Contributes
	if c.Icons != "assets/icons/" || len(c.Themes) != 1 || len(c.Fonts) != 1 || len(c.Languages) != 1 || len(c.Prompts) != 1 || len(c.Help) != 1 {
		t.Errorf("contributes %+v", c)
	}
	for src, want := range map[string]string{
		"[contributes]\nthemes = [\"../x/*.toml\"]":        "isn't a path in the plugin",
		"[contributes]\nthemes = [\"/etc/*.toml\"]":        "isn't a path in the plugin",
		"[contributes]\nwidgets = [\"a\"]":                 `has no "widgets"`,
		"binaries = [\"plan9/386\"]":                       "isn't one of",
		"version = \"1.0\"\n[contributes]\nhelp = [\"a\"]": "isn't a full version",
	} {
		if !strings.Contains(src, "version") {
			src = "version = \"1.0.0\"\n" + src
		}
		_, err := ParseManifest([]byte("id = \"acme/x\"\n" + src))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", src, err, want)
		}
	}
}

// "oriel.asset(\"icons/x.svg\") needs no capability: it's the plugin's own directory" — under assets/,
// and a link can't lead out of it.
func TestAsset(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "assets", "icons"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "icons", "x.svg"), []byte("<svg/>"), 0o644)
	os.WriteFile(filepath.Join(dir, "secret"), []byte("no"), 0o644)
	os.Symlink(filepath.Join(dir, "secret"), filepath.Join(dir, "assets", "link"))
	if b, err := Asset(dir, "icons/x.svg"); err != nil || string(b) != "<svg/>" {
		t.Errorf("Asset = %q, %v", b, err)
	}
	for _, name := range []string{"../secret", "/etc/hostname", "link"} {
		if _, err := Asset(dir, name); err == nil {
			t.Errorf("%s: read, want refused", name)
		}
	}
}
