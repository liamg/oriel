package ossandbox

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// PL-2, live on macOS: sandbox-exec with the generated profile lets the process read and write its paths, and
// nothing else.
func TestSandboxExecConfines(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	work, other := t.TempDir(), t.TempDir()
	for _, d := range []string{work, other} {
		if err := os.WriteFile(d+"/f", []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := Profile{Write: []string{work}}
	ops := []string{"read:" + work + "/f", "write:" + work + "/g", "read:" + other + "/f", "write:" + other + "/g", "exec:/bin/echo",
		"tcp:example.com:443"}
	expect(t, ops, run(t, p, ops...), true, true, false, false, false, false)
	p.Exec = []string{"/bin/echo"}
	expect(t, ops[4:5], run(t, p, ops[4:5]...), true)
}

// PL-19's spike (catalog.md, Risks): a binary Oriel downloads is only ad-hoc signed and isn't quarantined. It's
// fetched over HTTP as an install fetches an archive, signed ad hoc again, and must still run under sandbox-exec.
func TestDownloadedAdhocBinaryRuns(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(bin) }))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, body, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", probe).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v: %s", err, out)
	}
	if out, _ := exec.Command("codesign", "--display", "--verbose", probe).CombinedOutput(); !strings.Contains(string(out), "Signature=adhoc") {
		t.Fatalf("not ad-hoc signed: %s", out)
	}
	if err := exec.Command("xattr", "-p", "com.apple.quarantine", probe).Run(); err == nil {
		t.Fatal("the download is quarantined")
	}
	work := t.TempDir()
	cmd, err := Command(Profile{Write: []string{work}}, probe, "write:"+work+"/f")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), probeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("the ad-hoc signed download under sandbox-exec: %v: %s", err, out)
	}
}
