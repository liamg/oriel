package zstd

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

const (
	golden       = "../spec/zstd/facebook-zstd/tests/golden-decompression"
	goldenErrors = "../spec/zstd/facebook-zstd/tests/golden-decompression-errors"
	goldenInputs = "../spec/zstd/facebook-zstd/tests/golden-compression"
)

func read(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// stream decodes src through a Reader, reading through r's wrapping of it.
func stream(src []byte, wrap func(io.Reader) io.Reader) ([]byte, error) {
	z, err := NewReader(wrap(bytes.NewReader(src)))
	if err != nil {
		return nil, err
	}
	defer z.Close()
	return io.ReadAll(z)
}

func plain(r io.Reader) io.Reader { return r }

// both decodes src with Decode and with a Reader, and fails unless they agree.
func both(t *testing.T, src []byte) ([]byte, error) {
	t.Helper()
	a, errA := Decode(src)
	b, errB := stream(src, plain)
	if (errA == nil) != (errB == nil) {
		t.Fatalf("Decode: %v; Reader: %v", errA, errB)
	}
	if errA == nil && !bytes.Equal(a, b) {
		t.Fatalf("Decode gave %d bytes, Reader %d, and they differ", len(a), len(b))
	}
	return a, errA
}

// The fixtures in testdata were made by the zstd CLI from their originals (testdata/SOURCE).
func fixtureOriginal(t *testing.T, name string) []byte {
	base := name[:strings.IndexByte(name, '.')]
	switch base {
	case "rle300k":
		return bytes.Repeat([]byte("a"), 300000)
	case "empty":
		return nil
	case "rollout":
		return read(t, "testdata/rollout.jsonl")
	case "random":
		return read(t, "testdata/random.bin")
	}
	return read(t, filepath.Join(goldenInputs, base))
}

// Every fixture decodes to its original, by Decode, by a Reader, and by a Reader fed a byte at a time.
//
// ref:   zstd/rfc8878.txt
// quote: "The content checksum is the result of the XXH64() hash function [XXHASH] digesting the original (decoded) data as input, and a seed of zero."
func TestFixtures(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.zst")
	if len(files) < 15 {
		t.Fatalf("found %d fixtures", len(files))
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			want := fixtureOriginal(t, name)
			src := read(t, f)
			got, err := both(t, src)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
			}
			if len(src) < 100<<10 {
				slow, err := stream(src, iotest.OneByteReader)
				if err != nil || !bytes.Equal(slow, want) {
					t.Fatalf("a byte at a time: %d bytes, %v", len(slow), err)
				}
			}
			half, err := stream(src, iotest.HalfReader)
			if err != nil || !bytes.Equal(half, want) {
				t.Fatalf("half reads: %d bytes, %v", len(half), err)
			}
		})
	}
}

// Codex compresses a cold rollout at level 3; that's the fixture a rollout is read like.
//
// ref:   codex/codex-rs/rollout/src/compression.rs
// quote: "const COMPRESSION_LEVEL: i32 = 3;"
func TestCodexLevel(t *testing.T) {
	got, err := Decode(read(t, "testdata/rollout.jsonl.3.zst"))
	if err != nil || !bytes.Equal(got, read(t, "testdata/rollout.jsonl")) {
		t.Fatalf("level 3 rollout: %d bytes, %v", len(got), err)
	}
}

// zstd's golden frames decode.
//
// ref:   zstd/facebook-zstd/tests/cli-tests/decompression/golden.sh
// quote: zstd -r -t "$GOLDEN_DIR"
func TestGolden(t *testing.T) {
	files, _ := filepath.Glob(golden + "/*.zst")
	if len(files) == 0 {
		t.Fatal("no golden frames")
	}
	want := map[string]string{} // by the reference CLI (testdata/SOURCE)
	for line := range strings.Lines(string(read(t, "testdata/golden.sha256"))) {
		sum, name, _ := strings.Cut(strings.TrimSpace(line), "  ")
		want[name] = sum
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			got, err := both(t, read(t, f))
			if err != nil {
				t.Fatal(err)
			}
			if sum := fmt.Sprintf("%x", sha256.Sum256(got)); sum != want[filepath.Base(f)] {
				t.Fatalf("sha256 %s, want %s", sum, want[filepath.Base(f)])
			}
		})
	}
}

// zstd's golden bad frames are refused.
//
// ref:   zstd/facebook-zstd/tests/cli-tests/decompression/detectErrors.sh
// quote: zstd -t $file && die "should have detected an error"
func TestGoldenErrors(t *testing.T) {
	files, _ := filepath.Glob(goldenErrors + "/*.zst")
	if len(files) == 0 {
		t.Fatal("no golden bad frames")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			if _, err := both(t, read(t, f)); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err = %v, want ErrCorrupt", err)
			}
		})
	}
}

// Frames follow one another, skippable ones skipped wherever they are.
//
// ref:   zstd/rfc8878.txt
// quote: "From a compliant decoder perspective, skippable frames simply need to be skipped, and their content ignored, resuming decoding after the skippable frame."
// ref:   zstd/rfc8878.txt
// quote: "Value: 0x184D2A5?, which means any value from 0x184D2A50 to 0x184D2A5F."
func TestConcatenated(t *testing.T) {
	a := read(t, "testdata/http.3.zst")
	b := read(t, "testdata/rollout.jsonl.1.zst")
	skip := func(magic uint32, data string) []byte {
		f := binary.LittleEndian.AppendUint32(nil, magic)
		f = binary.LittleEndian.AppendUint32(f, uint32(len(data)))
		return append(f, data...)
	}
	var src []byte
	src = append(src, skip(0x184D2A50, "first")...)
	src = append(src, a...)
	src = append(src, skip(0x184D2A5F, "")...)
	src = append(src, b...)
	src = append(src, a...)
	src = append(src, skip(0x184D2A57, "last")...)
	got, err := both(t, src)
	if err != nil {
		t.Fatal(err)
	}
	h, r := read(t, goldenInputs+"/http"), read(t, "testdata/rollout.jsonl")
	want := append(append(append([]byte{}, h...), r...), h...)
	if !bytes.Equal(got, want) {
		t.Fatalf("decoded %d bytes, want %d", len(got), len(want))
	}
	if got, err := both(t, skip(0x184D2A53, "only")); err != nil || len(got) != 0 {
		t.Errorf("a lone skippable frame: %q, %v", got, err)
	}
	if got, err := both(t, nil); err != nil || len(got) != 0 {
		t.Errorf("no input: %q, %v", got, err)
	}
	if _, err := both(t, skip(0x184D2A50, "data")[:9]); !errors.Is(err, ErrCorrupt) {
		t.Errorf("a cut skippable frame: %v", err)
	}
}

// frame builds a frame: its header descriptor and the fields after it, then blocks, then a checksum if asked.
type frame struct {
	fhd    byte
	fields []byte
	blocks [][]byte
}

func (f frame) bytes(content []byte) []byte {
	out := binary.LittleEndian.AppendUint32(nil, frameMagic)
	out = append(out, f.fhd)
	out = append(out, f.fields...)
	for _, b := range f.blocks {
		out = append(out, b...)
	}
	if f.fhd&0x04 != 0 {
		out = binary.LittleEndian.AppendUint32(out, uint32(sum64(content)))
	}
	return out
}

// block is a block header and its content.
func block(last bool, typ, size int, content []byte) []byte {
	v := size<<3 | typ<<1
	if last {
		v |= 1
	}
	return append([]byte{byte(v), byte(v >> 8), byte(v >> 16)}, content...)
}

// Hand-built frames of raw and RLE blocks, with each way of giving the content size and window.
//
// ref:   zstd/rfc8878.txt
// quote: "When FCS_Field_Size is 1, 4, or 8 bytes, the value is read directly. When FCS_Field_Size is 2, the offset of 256 is added."
// ref:   zstd/rfc8878.txt
// quote: "RLE_Block: This is a single byte, repeated Block_Size times."
// ref:   zstd/rfc8878.txt
// quote: "windowAdd = (windowBase / 8) * Mantissa;"
func TestHandBuilt(t *testing.T) {
	hello := []byte("hello, world")
	long := append(bytes.Repeat([]byte("z"), 300), hello...)
	cases := []struct {
		name    string
		f       frame
		content []byte
	}{
		{"single segment, 1-byte size", frame{0x20, []byte{12}, [][]byte{block(true, blockRaw, 12, hello)}}, hello},
		{"2-byte size", frame{0x60, []byte{byte(312 - 256), 0}, [][]byte{block(false, blockRLE, 300, []byte("z")), block(true, blockRaw, 12, hello)}}, long},
		{"4-byte size, checksum", frame{0xA4, []byte{12, 0, 0, 0}, [][]byte{block(true, blockRaw, 12, hello)}}, hello},
		{"8-byte size", frame{0xE0, []byte{12, 0, 0, 0, 0, 0, 0, 0}, [][]byte{block(true, blockRaw, 12, hello)}}, hello},
		{"no size, window 1.25KB", frame{0x04, []byte{0x02}, [][]byte{block(false, blockRaw, 5, hello[:5]), block(true, blockRaw, 7, hello[5:])}}, hello},
		{"empty last block", frame{0x00, []byte{0x00}, [][]byte{block(false, blockRaw, 12, hello), block(true, blockRaw, 0, nil)}}, hello},
		{"dictionary id 0", frame{0x21, []byte{0, 12}, [][]byte{block(true, blockRaw, 12, hello)}}, hello},
		{"unused bit", frame{0x30, []byte{12}, [][]byte{block(true, blockRaw, 12, hello)}}, hello},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := both(t, c.f.bytes(c.content))
			if err != nil || !bytes.Equal(got, c.content) {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	h, err := parseHeader([]byte{0x00, 3<<3 | 5})
	if err != nil || h.window != 1<<13+1<<10*5 {
		t.Errorf("window = %d, %v", h.window, err)
	}
}

// What a frame may not be.
//
// ref:   zstd/rfc8878.txt
// quote: "A decoder compliant with this specification version must ensure it is not set."
// ref:   zstd/rfc8878.txt
// quote: "If such a value is present, it is considered to be corrupt data, and a compliant decoder must reject it."
// ref:   zstd/rfc8878.txt
// quote: "a decoder is allowed to reject a compressed frame that requests a memory size beyond the decoder's authorized range."
// ref:   zstd/rfc8878.txt
// quote: "This maximum is applicable to both the decompressed size and the compressed size of any block in the frame."
func TestRefused(t *testing.T) {
	hello := []byte("hello")
	ok := block(true, blockRaw, 5, hello)
	cases := []struct {
		name string
		src  []byte
		want error
	}{
		{"reserved bit", frame{0x28, []byte{5}, [][]byte{ok}}.bytes(hello), ErrCorrupt},
		{"reserved block type", frame{0x20, []byte{5}, [][]byte{block(true, blockReserved, 5, hello)}}.bytes(hello), ErrCorrupt},
		{"dictionary", frame{0x21, []byte{7, 5}, [][]byte{ok}}.bytes(hello), ErrDictionary},
		{"4-byte dictionary", frame{0x23, []byte{1, 0, 0, 0x80, 5}, [][]byte{ok}}.bytes(hello), ErrDictionary},
		{"window too large", frame{0x00, []byte{18 << 3}, [][]byte{ok}}.bytes(hello), ErrWindow},
		{"content size too large", frame{0xA0, []byte{0, 0, 0, 0x10}, [][]byte{ok}}.bytes(hello), ErrWindow},
		{"block over the window", frame{0x20, []byte{4}, [][]byte{block(true, blockRaw, 5, hello)}}.bytes(hello), ErrCorrupt},
		{"block over 128KB", frame{0x00, []byte{10 << 3}, [][]byte{block(true, blockRLE, 128<<10+1, []byte("a"))}}.bytes(nil), ErrCorrupt},
		{"size larger than content", frame{0x20, []byte{6}, [][]byte{ok}}.bytes(hello), ErrCorrupt},
		{"size smaller than content", frame{0x20, []byte{4}, [][]byte{block(true, blockRaw, 4, hello[:4]), ok}}.bytes(hello), ErrCorrupt},
		{"checksum", frame{0x24, []byte{5}, [][]byte{ok}}.bytes([]byte("hellO")), ErrChecksum},
		{"no last block", frame{0x20, []byte{5}, [][]byte{block(false, blockRaw, 5, hello)}}.bytes(hello), ErrCorrupt},
		{"bad magic", []byte{0x29, 0xB5, 0x2F, 0xFD, 0x20, 0}, ErrCorrupt},
		{"short magic", []byte{0x28, 0xB5}, ErrCorrupt},
		{"trailing garbage", append(frame{0x20, []byte{5}, [][]byte{ok}}.bytes(hello), 1, 2, 3, 4, 5), ErrCorrupt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := both(t, c.src); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	src := read(t, "testdata/http.3.zst")
	src[len(src)-1] ^= 1
	if _, err := both(t, src); !errors.Is(err, ErrChecksum) {
		t.Errorf("a changed checksum: %v", err)
	}
}

// Every cut of a frame is refused, and none panics.
func TestTruncated(t *testing.T) {
	for _, name := range []string{"testdata/http.3.zst", "testdata/huffman-compressed-larger.19.zst", golden + "/rle-first-block.zst"} {
		src := read(t, name)
		for n := range len(src) {
			if n == 0 {
				continue
			}
			if _, err := both(t, src[:n]); err == nil {
				t.Fatalf("%s cut to %d bytes decoded", name, n)
			}
		}
	}
}

// A Reader keeps about its frame's window, not the whole content.
//
// ref:   zstd/rfc8878.txt
// quote: "To properly decode compressed data, a decoder will need to allocate a buffer of at least Window_Size bytes."
func TestReaderWindow(t *testing.T) {
	src := read(t, "testdata/rollout.jsonl.wlog10.zst")
	z, err := NewReader(bytes.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	if z.h.window != 1<<10 {
		t.Fatalf("window = %d", z.h.window)
	}
	var out bytes.Buffer
	most := 0
	buf := make([]byte, 4096)
	for {
		n, err := z.Read(buf)
		out.Write(buf[:n])
		most = max(most, cap(z.hist))
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(out.Bytes(), read(t, "testdata/rollout.jsonl")) {
		t.Fatal("content differs")
	}
	if most > 1<<20 {
		t.Errorf("history grew to %d bytes for a 1KB window", most)
	}
	z.Close()
	if _, err := z.Read(buf); !errors.Is(err, ErrClosed) {
		t.Errorf("read after close: %v", err)
	}
}

func FuzzDecode(f *testing.F) {
	for _, dir := range []string{"testdata/*.zst", golden + "/*.zst", goldenErrors + "/*.zst"} {
		files, _ := filepath.Glob(dir)
		for _, p := range files {
			b := read(f, p)
			f.Add(b[:min(len(b), 4<<10)]) // small seeds keep mutating and minimising quick
		}
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		a, errA := Decode(src)
		b, errB := stream(src, plain)
		if (errA == nil) != (errB == nil) {
			t.Fatalf("Decode: %v; Reader: %v", errA, errB)
		}
		if errA == nil && !bytes.Equal(a, b) {
			t.Fatal("Decode and Reader differ")
		}
	})
}

// benchInput is several rollouts compressed as Codex does, one after another: a few MB.
func benchInput(b *testing.B) ([]byte, int) {
	one := read(b, "testdata/rollout.jsonl.3.zst")
	var src []byte
	for range 6 {
		src = append(src, one...)
	}
	return src, 6 * len(read(b, "testdata/rollout.jsonl"))
}

func BenchmarkDecode(b *testing.B) {
	src, n := benchInput(b)
	b.SetBytes(int64(n))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Decode(src); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReader(b *testing.B) {
	src, n := benchInput(b)
	b.SetBytes(int64(n))
	b.ReportAllocs()
	buf := make([]byte, 32<<10)
	for b.Loop() {
		z, err := NewReader(bytes.NewReader(src))
		if err != nil {
			b.Fatal(err)
		}
		for {
			_, err := z.Read(buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatal(err)
			}
		}
	}
}
