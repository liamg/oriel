package osclip

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// fakeXclip puts an xclip on PATH whose clipboard offers types, each target printing its file in dir.
func fakeXclip(t *testing.T, types string, content map[string]string) {
	dir := t.TempDir()
	for mime, body := range content {
		os.WriteFile(filepath.Join(dir, filepath.Base(mime)), []byte(body), 0o644)
	}
	script := "#!/bin/sh\nif [ \"$4\" = TARGETS ]; then printf '" + types + "'; exit 0; fi\nexec /bin/cat \"" + dir + "/${4##*/}\"\n"
	if err := os.WriteFile(filepath.Join(dir, "xclip"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("WAYLAND_DISPLAY", "")
}

func TestLinuxClipboardFilesAndImage(t *testing.T) {
	fakeXclip(t, "TARGETS\\ntext/uri-list\\nimage/png\\n", map[string]string{
		"text/uri-list": "file:///home/ada/a.go\nfile:///home/ada/shot.png\n",
		"image/png":     "\x89PNG fake",
	})
	if got := Files(); !slices.Equal(got, []string{"/home/ada/a.go", "/home/ada/shot.png"}) {
		t.Errorf("Files = %q", got)
	}
	if b, ok := Image(); !ok || string(b) != "\x89PNG fake" {
		t.Errorf("Image = %q, %v", b, ok)
	}
}

// What the clipboard doesn't offer isn't read: text alone is neither files nor a picture.
func TestLinuxClipboardOfOnlyText(t *testing.T) {
	fakeXclip(t, "TARGETS\\nUTF8_STRING\\n", nil)
	if got := Files(); got != nil {
		t.Errorf("Files = %q", got)
	}
	if _, ok := Image(); ok {
		t.Error("an image from a clipboard of text")
	}
}
