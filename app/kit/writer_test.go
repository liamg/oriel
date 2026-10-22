package kit

import (
	"os"
	"path/filepath"
	"testing"
)

// Writes land off the caller, each path's latest content last, in folders made for them.
func TestWriterWritesTheLatest(t *testing.T) {
	var w Writer
	p := filepath.Join(t.TempDir(), "a", "b.json")
	for _, s := range []string{"one", "two", "three"} {
		w.Write(p, []byte(s))
	}
	w.Wait()
	if b, err := os.ReadFile(p); err != nil || string(b) != "three" {
		t.Fatalf("read %q, %v; want three", b, err)
	}
	w.Remove(p)
	w.Wait()
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("after Remove: %v", err)
	}
}
