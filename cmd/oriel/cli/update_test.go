package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/update"
)

func TestAppBundle(t *testing.T) {
	b, inner, ok := appBundle("/Applications/Oriel.app/Contents/MacOS/oriel")
	if !ok || b != "/Applications/Oriel.app" || inner != "Contents/MacOS/oriel" {
		t.Errorf("appBundle = %q, %q, %v", b, inner, ok)
	}
	if _, _, ok := appBundle("/usr/local/bin/oriel"); ok {
		t.Error("a plain binary is a bundle")
	}
}

// `oriel update` in a build CI didn't stamp says it can't, rather than checking.
func TestUpdateNeedsAReleaseKey(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"update", "--check"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "no release key") {
		t.Errorf("oriel update = %d, %q", code, errOut.String())
	}
}

// `oriel update` checks, then installs, then says it's waiting for a restart.
func TestUpdateSelf(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	dir, root := t.TempDir(), t.TempDir()
	body := []byte("oriel 2.0.0")
	a := update.Describe("oriel", update.Platform(), update.KindBinary, body)
	rel, err := update.Sign(key, update.Manifest{Version: "2.0.0", Artifacts: []update.Artifact{a}})
	if err != nil {
		t.Fatal(err)
	}
	if err := update.Write(dir, rel, map[string][]byte{"oriel": body}); err != nil {
		t.Fatal(err)
	}
	in := &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"),
		Probe: func(context.Context, string, string) error { return nil }}
	if err := os.WriteFile(in.Target, []byte("oriel 1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &update.Updater{Source: update.Dir(dir), Keys: []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, Version: "1.0.0",
		Installer: in, CacheDir: t.TempDir()}
	ctx := context.Background()
	for _, step := range []struct {
		check bool
		want  string
	}{
		{true, "Oriel 2.0.0 is out (you have 1.0.0)"},
		{false, "Oriel 2.0.0 is installed: restart Oriel to run it"},
		{false, "Oriel 2.0.0 is installed: restart Oriel to run it"},
	} {
		var out bytes.Buffer
		if err := updateSelf(ctx, u, update.Stable, step.check, &out); err != nil || !strings.Contains(out.String(), step.want) {
			t.Fatalf("check=%v: %q, %v; want %q", step.check, out.String(), err, step.want)
		}
	}
	if b, _ := os.ReadFile(in.Target); string(b) != string(body) {
		t.Fatalf("installed %q", b)
	}
}
