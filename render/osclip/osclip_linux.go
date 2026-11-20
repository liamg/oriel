package osclip

import (
	"bytes"
	"context"
	"fmt"
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

// setImage puts png on the clipboard with wl-copy or xclip, which each stay behind in the background to serve it.
func setImage(png []byte) error {
	var cmd []string
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = []string{"wl-copy", "--type", "image/png"}
		}
	}
	if cmd == nil {
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = []string{"xclip", "-selection", "clipboard", "-t", "image/png", "-i"}
		}
	}
	if cmd == nil {
		return errNoImages
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Stdin = bytes.NewReader(png)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", cmd[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

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
