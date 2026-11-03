package runner

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

// The container's oriel is the helper the app carries, written out once per build; without one there's nothing to
// run, and the backend says so.
func TestOrielBinaryIsTheCarriedHelper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORIEL_SANDBOX_BIN", "")
	defer SetHelper(nil)

	SetHelper(nil)
	if _, err := OrielBinary(); err == nil {
		t.Error("no helper, but a binary")
	}
	if p, ok := helperProblem(); ok || !p.Blocking {
		t.Errorf("no helper isn't a blocking problem: %+v", p)
	}
	SetHelper(func() ([]byte, error) { return nil, errors.New("not embedded") })
	if _, err := OrielBinary(); err == nil || err.Error() != "not embedded" {
		t.Errorf("a helper that failed: %v", err)
	}

	SetHelper(func() ([]byte, error) { return []byte("helper v1"), nil })
	p1, err := OrielBinary()
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p1); !bytes.Equal(b, []byte("helper v1")) {
		t.Errorf("wrote %q", b)
	}
	if fi, _ := os.Stat(p1); fi.Mode()&0o111 == 0 {
		t.Errorf("not executable: %v", fi.Mode())
	}
	if again, _ := OrielBinary(); again != p1 {
		t.Errorf("the same build wrote a second file: %s", again)
	}
	SetHelper(func() ([]byte, error) { return []byte("helper v2"), nil })
	if p2, _ := OrielBinary(); p2 == p1 {
		t.Error("a new build overwrote the file running containers have mounted")
	}

	t.Setenv("ORIEL_SANDBOX_BIN", "/opt/oriel-sandbox")
	if p, _ := OrielBinary(); p != "/opt/oriel-sandbox" {
		t.Errorf("ORIEL_SANDBOX_BIN: %s", p)
	}
}
