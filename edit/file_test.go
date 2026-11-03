package edit

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDecodeEncode(t *testing.T) {
	for _, tt := range []struct {
		name, file, text string
		f                Format
		back             string // what Encode writes back; the file itself when empty
	}{
		{"lf", "a\nb\n", "a\nb", Format{}, ""},
		{"crlf", "a\r\nb\r\n", "a\nb", Format{CRLF: true}, ""},
		{"mixed endings are lf", "a\r\nb\n", "a\r\nb", Format{}, ""},
		{"bom", "\xef\xbb\xbfa\n", "a", Format{BOM: true}, ""},
		{"bom and crlf", "\xef\xbb\xbfa\r\nb\r\n", "a\nb", Format{BOM: true, CRLF: true}, ""},
		{"no newline at the end gets one", "a\nb", "a\nb", Format{}, "a\nb\n"},
		{"crlf without the last newline", "a\r\nb", "a\nb", Format{CRLF: true}, "a\r\nb\r\n"},
		{"one empty line", "\n", "", Format{}, ""},
	} {
		text, f := Decode([]byte(tt.file))
		if string(text) != tt.text || f != tt.f {
			t.Errorf("%s: Decode = %q %+v, want %q %+v", tt.name, text, f, tt.text, tt.f)
		}
		want := tt.back
		if want == "" {
			want = tt.file
		}
		if got := string(Encode(text, f, false)); got != want {
			t.Errorf("%s: Encode = %q, want %q", tt.name, got, want)
		}
	}
}

// A buffer with no lines at all writes nothing; one empty line writes its newline.
func TestEncodeNoLines(t *testing.T) {
	if got := Encode(nil, Format{}, true); len(got) != 0 {
		t.Errorf("no lines = %q", got)
	}
	if got := Encode(nil, Format{BOM: true}, true); string(got) != "\xef\xbb\xbf" {
		t.Errorf("no lines with a BOM = %q", got)
	}
	if got := Encode(nil, Format{CRLF: true}, false); string(got) != "\r\n" {
		t.Errorf("one empty line = %q", got)
	}
}

func TestReadOnlyFiles(t *testing.T) {
	dir := t.TempDir()
	for name, b := range map[string]string{"bin": "a\x00b", "latin1": "caf\xe9\n"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(b), 0o644)
		text, f, err := ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if f.ReadOnly == "" || string(text) != b {
			t.Errorf("%s: read-only %q, text %q", name, f.ReadOnly, text)
		}
		if err := f.Write([]byte("x"), false); err == nil {
			t.Errorf("%s: wrote a read-only file", name)
		}
	}
	_, f, err := ReadFile(filepath.Join(dir, "missing"))
	if err != nil || !f.New {
		t.Errorf("missing file: %+v, %v", f, err)
	}
}

// Writes are atomic and keep the file's mode; a symlink's target is written, and the link stays.
func TestWriteKeepsModeAndLinks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "run.sh")
	os.WriteFile(p, []byte("echo hi\r\n"), 0o750)
	link := filepath.Join(dir, "link.sh")
	os.Symlink(p, link)
	text, f, err := ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Write(append(text, "\necho bye"...), false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "echo hi\r\necho bye\r\n" {
		t.Errorf("file = %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o750 {
		t.Errorf("mode = %v", st.Mode())
	}
	if st, _ := os.Lstat(link); st.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*oriel*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestChangedOnDisk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(p, []byte("one\n"), 0o644)
	_, f, _ := ReadFile(p)
	if ch, _ := f.ChangedOnDisk(); ch {
		t.Error("changed straight after reading")
	}
	os.Chtimes(p, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	if ch, _ := f.ChangedOnDisk(); ch {
		t.Error("a touch with the same bytes is a change")
	}
	os.WriteFile(p, []byte("two\n"), 0o644)
	if ch, _ := f.ChangedOnDisk(); !ch {
		t.Error("new bytes aren't a change")
	}
	os.Remove(p)
	if ch, _ := f.ChangedOnDisk(); !ch {
		t.Error("deleting isn't a change")
	}
}
