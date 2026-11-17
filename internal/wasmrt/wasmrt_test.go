package wasmrt_test

import (
	"testing"

	"github.com/liamg/oriel/internal/wasmrt"
)

// Once the runtime is made its cache dir is fixed: the same dir again is fine, another is refused, not dropped
// silently.
func TestSetCacheDirAfterTheRuntimeIsMade(t *testing.T) {
	r := wasmrt.Runtime{Name: "test"}
	dir := t.TempDir()
	if err := r.SetCacheDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCacheDir(dir); err != nil {
		t.Errorf("the dir it runs with: %v", err)
	}
	if err := r.SetCacheDir(t.TempDir()); err == nil {
		t.Error("another dir, after the runtime was made, wasn't refused")
	}
}

// A module compiles once: the second use of its sum doesn't read the wasm again.
func TestAModuleCompilesOnce(t *testing.T) {
	r := wasmrt.Runtime{Name: "test"}
	reads := 0
	// The smallest valid module: the magic number and version 1.
	wasm := func() ([]byte, error) { reads++; return []byte{0, 'a', 's', 'm', 1, 0, 0, 0}, nil }
	for range 2 {
		if _, _, err := r.Compile([32]byte{1}, wasm); err != nil {
			t.Fatal(err)
		}
	}
	if reads != 1 {
		t.Errorf("the wasm was read %d times", reads)
	}
}
