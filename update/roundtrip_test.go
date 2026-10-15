//go:build unix

package update_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liamg/oriel/update"
	"github.com/liamg/oriel/wire"
)

// built is the test worker built once per version and health, for every test.
var built sync.Map

// buildWorker builds update/internal/testworker at version, trusting pub, and healthy or not after a restart.
func buildWorker(t *testing.T, pub ed25519.PublicKey, version string, healthy bool) []byte {
	t.Helper()
	key := version + "/" + map[bool]string{true: "ok", false: "sick"}[healthy] + "/" + hex.EncodeToString(pub)
	if b, ok := built.Load(key); ok {
		return b.([]byte)
	}
	out := filepath.Join(t.TempDir(), "oriel")
	flags := "-X main.version=" + version + " -X main.healthy=" + map[bool]string{true: "true", false: "false"}[healthy] +
		" -X github.com/liamg/oriel/update.releaseKeys=" + hex.EncodeToString(pub)
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "-ldflags", flags, "./internal/testworker")
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

// app is the test's side of the wire: it accepts the worker's connections and publishes releases to it.
type app struct {
	t     *testing.T
	key   ed25519.PrivateKey
	pub   *update.Publisher
	ln    net.Listener
	conns chan wire.Conn
}

func newApp(t *testing.T, key ed25519.PrivateKey) *app {
	// A unix socket's path is short; the test's temp dir can be too long for one.
	dir, err := os.MkdirTemp("", "upd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	a := &app{t: t, key: key, pub: &update.Publisher{Keys: []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}}, ln: ln,
		conns: make(chan wire.Conn, 4)}
	go func() {
		for {
			raw, err := ln.Accept()
			if err != nil {
				return
			}
			c, err := wire.Server(raw)
			if err != nil {
				raw.Close()
				continue
			}
			a.pub.Serve(c)
			a.conns <- c
		}
	}()
	return a
}

// next is the worker's next connection: it dials in when it starts, and again after each restart.
func (a *app) next() wire.Conn {
	a.t.Helper()
	select {
	case c := <-a.conns:
		return c
	case <-time.After(30 * time.Second):
		a.t.Fatal("the worker didn't dial in")
		return nil
	}
}

// release signs a release of version with one artifact of kind for this platform.
func (a *app) release(version, kind string, body []byte) (update.Release, update.Opener) {
	a.t.Helper()
	art := update.Describe("oriel-"+strings.ReplaceAll(update.Platform(), "/", "-"), update.Platform(), kind, body)
	rel, err := update.Sign(a.key, update.Manifest{Version: version, Channel: update.Stable, Artifacts: []update.Artifact{art}})
	if err != nil {
		a.t.Fatal(err)
	}
	dir := update.Dir(a.t.TempDir())
	if err := update.Write(string(dir), rel, map[string][]byte{art.Name: body}); err != nil {
		a.t.Fatal(err)
	}
	m, _ := rel.Verify(a.pub.Keys)
	return rel, update.Cache{Dir: a.t.TempDir(), Source: dir}.Opener(m)
}

// startWorker runs the installed worker as its own process, as a restart would find it.
func startWorker(t *testing.T, a *app, in *update.Installer) {
	t.Helper()
	args := []string{"serve", "-socket", a.ln.Addr().String(), "-dir", in.Dir, "-target", in.Target}
	if in.Exe != "" {
		args = append(args, "-exe", in.Exe)
	}
	cmd := exec.Command(in.Executable(), args...)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("worker log:\n%s", log.String())
		}
	})
}

func status(t *testing.T, c wire.Conn) update.Status {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st, err := update.StatusOf(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// push offers rel to the worker on c and swaps it in; the worker restarts onto it.
func push(t *testing.T, a *app, c wire.Conn, rel update.Release, open update.Opener) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := a.pub.Push(ctx, c, rel, open); err != nil {
		t.Fatalf("push: %v", err)
	}
	if err := update.Swap(ctx, c); err != nil {
		t.Fatalf("swap: %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("the worker didn't restart after the swap")
	}
}

// An update round-trips to a worker over the wire: v1 takes v2 and restarts on it, then takes a v3 that fails its
// health check after the restart and comes back on v2 by itself.
//
// ref:   go/src/os/file.go#Rename
// quote: "If newpath already exists and is not a directory, Rename replaces it."
func TestWorkerUpdateRoundTripsAndRollsBack(t *testing.T) {
	key := testKey(t)
	pub := key.Public().(ed25519.PublicKey)
	v1, v2, v3 := buildWorker(t, pub, "1.0.0", true), buildWorker(t, pub, "2.0.0", true), buildWorker(t, pub, "3.0.0", false)

	root := t.TempDir()
	in := &update.Installer{Target: filepath.Join(root, "bin", "oriel"), Dir: filepath.Join(root, "updates")}
	if err := os.MkdirAll(filepath.Dir(in.Target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in.Target, v1, 0o755); err != nil {
		t.Fatal(err)
	}
	a := newApp(t, key)
	startWorker(t, a, in)

	c := a.next()
	if st := status(t, c); st.Version != "1.0.0" || st.Platform != update.Platform() {
		t.Fatalf("worker started as %+v, want 1.0.0 on %s", st, update.Platform())
	}

	rel, open := a.release("2.0.0", update.KindBinary, v2)
	push(t, a, c, rel, open)
	c = a.next()
	st := status(t, c)
	if st.Version != "2.0.0" || st.Pending != "" || st.RolledBack != nil {
		t.Fatalf("after the update the worker is %+v, want 2.0.0 confirmed", st)
	}
	if b, _ := os.ReadFile(in.Target); !bytes.Equal(b, v2) {
		t.Fatal("the installed binary isn't v2")
	}
	if b, _ := os.ReadFile(in.Previous()); !bytes.Equal(b, v1) {
		t.Fatal("v1 wasn't kept as the previous version")
	}

	rel, open = a.release("3.0.0", update.KindBinary, v3)
	push(t, a, c, rel, open)
	c = a.next()
	st = status(t, c)
	if st.Version != "2.0.0" {
		t.Fatalf("after a failed health check the worker runs %s, want 2.0.0", st.Version)
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
	key := testKey(t)
	pub := key.Public().(ed25519.PublicKey)
	v1, v2, v3 := buildWorker(t, pub, "1.0.0", true), buildWorker(t, pub, "2.0.0", true), buildWorker(t, pub, "3.0.0", false)

	root := t.TempDir()
	exe := filepath.Join("Contents", "MacOS", "oriel")
	in := &update.Installer{Target: filepath.Join(root, "Applications", "Oriel.app"), Exe: exe, Dir: filepath.Join(root, "updates")}
	writeBundle(t, in.Target, v1)
	a := newApp(t, key)
	startWorker(t, a, in)

	c := a.next()
	rel, open := a.release("2.0.0", update.KindBundle, bundle(t, v2))
	push(t, a, c, rel, open)
	c = a.next()
	if st := status(t, c); st.Version != "2.0.0" || st.RolledBack != nil {
		t.Fatalf("after the update the app is %+v, want 2.0.0", st)
	}
	if b, _ := os.ReadFile(filepath.Join(in.Target, "Contents", "Info.plist")); string(b) != "2.0.0" {
		t.Fatalf("the bundle's Info.plist is %q, want the new bundle's", b)
	}

	rel, open = a.release("3.0.0", update.KindBundle, bundle(t, v3))
	push(t, a, c, rel, open)
	c = a.next()
	if st := status(t, c); st.Version != "2.0.0" || st.RolledBack == nil || st.RolledBack.Version != "3.0.0" {
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
