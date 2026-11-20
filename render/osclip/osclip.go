// Package osclip reads what the system clipboard holds besides text: copied files and pictures.
package osclip

import (
	"errors"
	"net/url"
	"strings"
)

// maxImage is the largest picture a paste takes.
const maxImage = 32 << 20

// Files are the paths of the files copied in the OS's file manager, if that's what the clipboard holds.
func Files() []string { return files() }

// SetImage puts png on the clipboard, in place of what it held.
func SetImage(png []byte) error {
	if len(png) == 0 {
		return errors.New("no picture to copy")
	}
	return setImage(png)
}

// errNoImages is a platform, or a display without a clipboard tool, that can't put a picture on the clipboard.
var errNoImages = errors.New("the clipboard here takes only text")

// Image is the picture the clipboard holds, as PNG.
func Image() ([]byte, bool) {
	b := image()
	return b, len(b) > 0
}

// uriList is the local paths in a text/uri-list: one URI a line, # comments skipped.
func uriList(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		u, err := url.Parse(ln)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		out = append(out, u.Path)
	}
	return out
}
