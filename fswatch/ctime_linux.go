package fswatch

import (
	"os"
	"syscall"
	"time"
)

// changedAt is when fi's file last changed, its contents or its name: its ctime.
func changedAt(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctim.Unix())
	}
	return fi.ModTime()
}

// madeAt is when fi's file was made, as far as this OS says: Linux keeps no birth time here, so its ctime.
func madeAt(fi os.FileInfo) time.Time { return changedAt(fi) }
