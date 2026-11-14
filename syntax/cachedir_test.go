package syntax

import "testing"

// Once the grammar runtime is made its cache dir is fixed: the same dir again is fine, another is refused, not
// dropped silently.
func TestSetCacheDirAfterTheRuntimeIsMade(t *testing.T) {
	if _, err := wasmRuntime(); err != nil {
		t.Fatal(err)
	}
	runtime.mu.Lock()
	dir := runtime.cacheDir
	runtime.mu.Unlock()
	if err := SetCacheDir(dir); err != nil {
		t.Errorf("the dir it runs with: %v", err)
	}
	if err := SetCacheDir(t.TempDir()); err == nil {
		t.Error("another dir, after the runtime was made, wasn't refused")
	}
}
