package stats

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/agent/codex"
)

// A rollout fixture and the zstd CLI's level-3 compression of it.
const (
	plainRollout      = "../../zstd/testdata/rollout.jsonl"
	compressedRollout = "../../zstd/testdata/rollout.jsonl.3.zst"
)

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A compressed rollout reads as its plain one does, by its own path and by the plain path it replaced.
//
// ref:   codex/codex-rs/rollout/src/compression.rs
// quote: "Opens a rollout line reader that transparently handles plain `.jsonl` and `.jsonl.zst` files."
// ref:   codex/codex-rs/rollout/src/compression.rs
// quote: "const COMPRESSION_LEVEL: i32 = 3;"
func TestCompressedRollout(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "rollout-2026-10-01T09-00-00-0199a213-81c0-7800-8aa1-bbab2a035a53.jsonl")
	copyFile(t, plainRollout, plain)
	want := New(plain, codex.UsageLog)
	if !want.Read() || want.Requests == 0 || want.Cwd != "/work/project" {
		t.Fatalf("plain rollout: %d requests, cwd %q", want.Requests, want.Cwd)
	}

	copyFile(t, compressedRollout, plain+".zst")
	got := New(plain+".zst", codex.UsageLog)
	if !got.Read() || got.Requests != want.Requests || got.Tokens != want.Tokens || got.ToolCalls != want.ToolCalls || got.Turns != want.Turns {
		t.Errorf("compressed: %d requests, %+v, %d calls; want %d, %+v, %d", got.Requests, got.Tokens, got.ToolCalls,
			want.Requests, want.Tokens, want.ToolCalls)
	}
	if fi, _ := os.Stat(plain + ".zst"); got.Size != fi.Size() {
		t.Errorf("size = %d, want the compressed file's %d", got.Size, fi.Size())
	}

	// Read up to its first half, then compressed whole in its place: the rest is read, from where the first left off.
	full, _ := os.ReadFile(plainRollout)
	half := full[:bytes.IndexByte(full[len(full)/2:], '\n')+len(full)/2+1]
	if err := os.WriteFile(plain, half, 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(plain, codex.UsageLog)
	s.Read()
	if s.Requests == 0 || s.Requests >= want.Requests {
		t.Fatalf("half the rollout: %d requests of %d", s.Requests, want.Requests)
	}
	os.Remove(plain)
	if !s.Read() || s.Requests != want.Requests || s.Tokens != want.Tokens || s.Offset != int64(len(full)) {
		t.Errorf("after compression: %d requests, %+v, offset %d; want %d, %+v, %d", s.Requests, s.Tokens, s.Offset,
			want.Requests, want.Tokens, len(full))
	}
	if s.Read() {
		t.Error("read the compressed rollout again")
	}

	// The store reads it by either path.
	st := NewStore("", nil, testFormats)
	if sc := st.Scan("a", []Log{{Path: plain + ".zst", Format: codex.UsageLog.Name}}); sc[0].Requests != want.Requests {
		t.Errorf("store scan: %d requests", sc[0].Requests)
	}
	if sc := st.Scan("b", []Log{{Path: plain, Format: codex.UsageLog.Name}}); sc[0].Requests != want.Requests {
		t.Errorf("store scan by the plain path: %d requests", sc[0].Requests)
	}

	// A file that isn't zstd reads as nothing.
	if err := os.WriteFile(plain+".zst", []byte("not zstd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if New(plain+".zst", codex.UsageLog).Read() {
		t.Error("read a .zst that isn't zstd")
	}
}
