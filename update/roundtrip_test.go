//go:build unix

package update_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/update"
)

// built is the test app built once per version and health, for every test.
var built sync.Map

// buildApp builds update/internal/testapp at version, healthy or not after a restart.
func buildApp(t *testing.T, version string, healthy bool) []byte {
	t.Helper()
	key := version + "/" + map[bool]string{true: "ok", false: "sick"}[healthy]
	if b, ok := built.Load(key); ok {
		return b.([]byte)
	}
	out := filepath.Join(t.TempDir(), "oriel")
	flags := "-X main.version=" + version + " -X main.healthy=" + map[bool]string{true: "true", false: "false"}[healthy]
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "-ldflags", flags, "./internal/testapp")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	built.Store(key, b)
	return b
}

type status struct {
	Version    string           `json:"version"`
	Pending    string           `json:"pending,omitempty"`
	RolledBack *update.Rollback `json:"rolledBack,omitempty"`
}

// start runs the installed app as a restart would find it, and reads back what it runs.
func start(t *testing.T, in *update.Installer) status {
	t.Helper()
	args := []string{"boot", "-dir", in.Dir, "-target", in.Target}
	if in.Exe != "" {
		args = append(args, "-exe", in.Exe)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, in.Executable(), args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start: %v\n%s", err, stderr.String())
	}
	var st status
	if err := json.Unmarshal(out, &st); err != nil {
		t.Fatalf("start printed %q: %v", out, err)
	}
	return st
}

// install stages version's artifact of kind and swaps it in, as Updater.Install does; it runs from the next start.
func install(t *testing.T, in *update.Installer, version, kind string, body []byte) {
	t.Helper()
	art := update.Describe("oriel-"+strings.ReplaceAll(update.Platform(), "/", "-"), update.Platform(), kind, body)
	m := update.Manifest{Version: version, Channel: update.Stable, Artifacts: []update.Artifact{art}}
	if err := in.Stage(context.Background(), m, art, bytes.NewReader(body)); err != nil {
		t.Fatalf("stage %s: %v", version, err)
	}
	if err := in.Swap(); err != nil {
		t.Fatalf("swap %s: %v", version, err)
	}
}

// An update round-trips: v1 installs v2 and restarts on it, then installs a v3 that fails its health check after
// the restart and comes back on v2 by itself.
//
// ref:   go/src/os/file.go#Rename
// quote: "If newpath already exists and is not a directory, Rename replaces it."
func TestUpdateRoundTripsAndRollsBack(t *testing.T) {
	v1, v2, v3 := buildApp(t, "1.0.0", true), buildApp(t, "2.0.0", true), buildApp(t, "3.0.0", false)

	root := t.TempDir()
	in := &update.Installer{Target: filepath.Join(root, "bin", "oriel"), Dir: filepath.Join(root, "updates")}
	if err := os.MkdirAll(filepath.Dir(in.Target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.Target, v1, 0o755); err != nil {
		t.Fatal(err)
	}
	if st := start(t, in); st.Version != "1.0.0" {
		t.Fatalf("started as %+v, want 1.0.0", st)
	}

	install(t, in, "2.0.0", update.KindBinary, v2)
	st := start(t, in)
	if st.Version != "2.0.0" || st.Pending != "" || st.RolledBack != nil {
		t.Fatalf("after the update it is %+v, want 2.0.0 confirmed", st)
	}
	if b, _ := os.ReadFile(in.Target); !bytes.Equal(b, v2) {
		t.Fatal("the installed binary isn't v2")
	}
	if b, _ := os.ReadFile(in.Previous()); !bytes.Equal(b, v1) {
		t.Fatal("v1 wasn't kept as the previous version")
	}

	install(t, in, "3.0.0", update.KindBinary, v3)
	st = start(t, in)
	if st.Version != "2.0.0" {
		t.Fatalf("after a failed health check it runs %s, want 2.0.0", st.Version)
	}
	if st.RolledBack == nil || st.RolledBack.Version != "3.0.0" || !strings.Contains(st.RolledBack.Reason, "unhealthy on purpose") {
		t.Fatalf("rollback = %+v, want 3.0.0 and its health check's error", st.RolledBack)
	}
	if b, _ := os.ReadFile(in.Target); !bytes.Equal(b, v2) {
		t.Fatal("the installed binary isn't v2 after the rollback")
	}
}

// The macOS app swaps its whole .app bundle, shipped as a .tar.gz, and rolls the bundle back the same way.
//
// ref:   go/src/os/file.go#Rename
// quote: "If newpath already exists and is a directory, Rename returns an error."
func TestBundleUpdateRoundTripsAndRollsBack(t *testing.T) {
	v1, v2, v3 := buildApp(t, "1.0.0", true), buildApp(t, "2.0.0", true), buildApp(t, "3.0.0", false)

	root := t.TempDir()
	exe := filepath.Join("Contents", "MacOS", "oriel")
	in := &update.Installer{Target: filepath.Join(root, "Applications", "Oriel.app"), Exe: exe, Dir: filepath.Join(root, "updates")}
	writeBundle(t, in.Target, v1)

	install(t, in, "2.0.0", update.KindBundle, bundle(t, v2))
	if st := start(t, in); st.Version != "2.0.0" || st.RolledBack != nil {
		t.Fatalf("after the update the app is %+v, want 2.0.0", st)
	}
	if b, _ := os.ReadFile(filepath.Join(in.Target, "Contents", "Info.plist")); string(b) != "2.0.0" {
		t.Fatalf("the bundle's Info.plist is %q, want the new bundle's", b)
	}

	install(t, in, "3.0.0", update.KindBundle, bundle(t, v3))
	if st := start(t, in); st.Version != "2.0.0" || st.RolledBack == nil || st.RolledBack.Version != "3.0.0" {
		t.Fatalf("after a failed health check the app is %+v, want 2.0.0 with 3.0.0 rolled back", st)
	}
	if b, _ := os.ReadFile(in.Executable()); !bytes.Equal(b, v2) {
		t.Fatal("the bundle's executable isn't v2 after the rollback")
	}
}

func writeBundle(t *testing.T, dir string, exe []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Contents", "MacOS", "oriel"), exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Contents", "Info.plist"), []byte("1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bundle is a .app bundle's .tar.gz, with exe as its executable and its version in Info.plist.
func bundle(t *testing.T, exe []byte) []byte {
	t.Helper()
	ver, err := versionOf(t, exe)
	if err != nil {
		t.Fatal(err)
	}
	return tarGz(t, []tarFile{
		{name: "Contents/", dir: true},
		{name: "Contents/MacOS/", dir: true},
		{name: "Contents/MacOS/oriel", body: exe, mode: 0o755},
		{name: "Contents/Info.plist", body: []byte(ver), mode: 0o644},
	})
}

// versionOf runs a built binary for the version it reports.
func versionOf(t *testing.T, exe []byte) (string, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "oriel")
	if err := os.WriteFile(p, exe, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(p, "version").Output()
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(out))
	if len(f) != 2 {
		return "", errors.New("no version")
	}
	return f[1], nil
}
