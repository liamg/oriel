package catalog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/plugin"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func problems(err error) []string {
	var pe *PackError
	if !errors.As(err, &pe) {
		return nil
	}
	var out []string
	for _, p := range pe.Problems {
		out = append(out, p.String())
	}
	return out
}

// Pack rejects a plugin with an undeclared capability: it checks that each statically visible oriel.* call's
// capability is declared, through an alias as plugins write it (local L =
// oriel), and the plugin's own process needs no exec grant.
func TestPackRejectsUndeclared(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"oriel.toml": "id = \"acme/x\"\nversion = \"1.0.0\"\n[permissions]\nnet = [\"api.example.com\"]\n",
		"init.lua": `local L = oriel
-- oriel.agent.pack("in a comment", {})
L.command("X", function() end)            -- ui: always allowed
local own = L.process.spawn("bin/x")       -- its own binary
L.http.request({ url = "https://api.example.com" }, function() end)
L.agent.tool{ name = "t", run = function() end }
local other = oriel.process.spawn("/usr/bin/git")
`,
		"lua/more.lua": "local O = oriel\nO.thread.list()\nlocal s = [[oriel.git.commit()]]\n",
	})
	_, err := Pack(context.Background(), dir, PackOpts{Out: t.TempDir()})
	got := problems(err)
	want := []string{
		"init.lua:6: oriel.agent.tool needs agent.tools, which oriel.toml doesn't declare",
		"init.lua:7: oriel.process.spawn needs exec, which oriel.toml doesn't declare",
		"lua/more.lua:2: oriel.thread.list needs agent.read, which oriel.toml doesn't declare",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Pack rejects a plugin over the size limit: each plugin can be at most 20 MB, unpacked.
func TestPackRejectsOversize(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"oriel.toml": "id = \"acme/big\"\nversion = \"1.0.0\"\n[contributes]\nfonts = [\"assets/fonts/*.ttf\"]\n",
	})
	os.MkdirAll(filepath.Join(dir, "assets", "fonts"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "assets", "fonts", "huge.ttf"), make([]byte, plugin.MaxSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Pack(context.Background(), dir, PackOpts{Out: t.TempDir()})
	if got := problems(err); len(got) != 1 || !strings.Contains(got[0], "a plugin may be at most 20.0 MB") {
		t.Errorf("problems %q, want the size limit", got)
	}
}

var sha = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Pack prints sha256s for a good plugin. A data-only plugin is one archive; the archive is
// reproducible, so the registry's CI can build it again and match.
func TestPackGood(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"oriel.toml":              "id = \"acme/dusk\"\nversion = \"1.0.0\"\n[contributes]\nthemes = [\"assets/themes/*.toml\"]\n",
		"assets/themes/dusk.toml": "name = \"dusk\"\n",
		".github/workflows/x.yml": "not packed",
	})
	out := t.TempDir()
	p, err := Pack(context.Background(), dir, PackOpts{Out: out})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Archives) != 1 || p.Archives[0].Platform != AnyPlatform || filepath.Base(p.Archives[0].Path) != "dusk-1.0.0.tar.gz" ||
		!sha.MatchString(p.Archives[0].SHA256) {
		t.Fatalf("archives %+v", p.Archives)
	}
	again, err := Pack(context.Background(), dir, PackOpts{Out: t.TempDir()})
	if err != nil || again.Archives[0].SHA256 != p.Archives[0].SHA256 {
		t.Errorf("packing again gave %v, %v; want the same sha256", again.Archives, err)
	}
	files, err := Unpack(p.Archives[0].Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, " ") != "assets/themes/dusk.toml oriel.toml" {
		t.Errorf("archive holds %v", files)
	}
}

// A process plugin's archive is per platform, its Go command built into bin/.
func TestPackProcessPlugin(t *testing.T) {
	dir := t.TempDir()
	if _, err := Scaffold(dir, "acme/echo", true); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	p, err := Pack(context.Background(), dir, PackOpts{Out: t.TempDir(), Platforms: []string{"linux/amd64"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Archives) != 1 || filepath.Base(p.Archives[0].Path) != "echo-0.1.0-linux-amd64.tar.gz" {
		t.Fatalf("archives %+v", p.Archives)
	}
	files, err := Unpack(p.Archives[0].Path, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(" "+strings.Join(files, " ")+" ", " bin/echo ") {
		t.Errorf("archive holds %v, want bin/echo", files)
	}
}
