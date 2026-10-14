package testrun

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// maxFingerprint is how many files a fingerprint looks at; past it, the code is taken to have changed.
const maxFingerprint = 50000

// Fingerprint is a run's Change: a hash of the files under root (their paths, sizes and times), leaving out
// dot-directories, vendor and node_modules. Two runs with the same fingerprint ran the same code, so a test that
// failed in one and passed in the other is flaky.
func Fingerprint(root string) string {
	h := sha256.New()
	n := 0
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && (strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if n++; n > maxFingerprint {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if n > maxFingerprint {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
