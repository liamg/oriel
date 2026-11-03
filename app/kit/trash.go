package kit

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Trash moves path to the user's trash: ~/.Trash on macOS, the freedesktop.org trash ($XDG_DATA_HOME/Trash) on
// Linux, with what restoring it needs. It moves, never copies: a path on another disk from the trash is refused.
func Trash(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return moveInto(filepath.Join(home, ".Trash"), path, nil)
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		data = filepath.Join(home, ".local", "share")
	}
	trash := filepath.Join(data, "Trash")
	return moveInto(filepath.Join(trash, "files"), path, func(name string) error {
		if err := os.MkdirAll(filepath.Join(trash, "info"), 0o700); err != nil {
			return err
		}
		info := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", (&url.URL{Path: path}).EscapedPath(), time.Now().Format("2006-01-02T15:04:05"))
		return os.WriteFile(filepath.Join(trash, "info", name+".trashinfo"), []byte(info), 0o600)
	})
}

// moveInto moves path into dir under a name not taken there, after note records it under that name.
func moveInto(dir, path string, note func(name string) error) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := filepath.Base(path)
	name := base
	for i := 2; ; i++ {
		if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		ext := filepath.Ext(base)
		name = fmt.Sprintf("%s %d%s", strings.TrimSuffix(base, ext), i, ext)
	}
	if note != nil {
		if err := note(name); err != nil {
			return err
		}
	}
	if err := os.Rename(path, filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("couldn't move %s to the trash: %w", base, err)
	}
	return nil
}
