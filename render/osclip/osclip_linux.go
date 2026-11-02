package osclip

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// A clipboard tool for the display: wl-paste on Wayland, xclip on X11. Without one, the clipboard holds only text.

func files() []string {
	b := read("text/uri-list")
	if len(b) == 0 {
		b = read("x-special/gnome-copied-files") // "copy\nfile:///…"
	}
	return uriList(string(b))
}

func image() []byte { return read("image/png") }

// read is the clipboard's content of type mime, if it offers it.
func read(mime string) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	list, get := commands(mime)
	if list == nil {
		return nil
	}
	types, err := exec.CommandContext(ctx, list[0], list[1:]...).Output()
	if err != nil || !slices.Contains(strings.Fields(string(types)), mime) {
		return nil
	}
	cmd := exec.CommandContext(ctx, get[0], get[1:]...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	var b bytes.Buffer
	_, err = io.Copy(&b, io.LimitReader(out, maxImage+1))
	if werr := cmd.Wait(); err != nil || werr != nil || b.Len() > maxImage {
		return nil
	}
	return b.Bytes()
}

// commands are the tool's commands that list the clipboard's types and read one, for the display Oriel is on.
func commands(mime string) (list, get []string) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-paste"); err == nil {
			return []string{"wl-paste", "--list-types"}, []string{"wl-paste", "--no-newline", "--type", mime}
		}
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return []string{"xclip", "-selection", "clipboard", "-t", "TARGETS", "-o"}, []string{"xclip", "-selection", "clipboard", "-t", mime, "-o"}
	}
	return nil, nil
}
