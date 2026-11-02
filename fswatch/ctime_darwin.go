package fswatch

import (
	"os"
	"syscall"
	"time"
)

// changedAt is when fi's file last changed, its contents or its name: its ctime.
func changedAt(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctimespec.Unix())
	}
	return fi.ModTime()
}

// madeAt is when fi's file was made, as far as this OS says.
func madeAt(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Unix())
	}
	return changedAt(fi)
}
