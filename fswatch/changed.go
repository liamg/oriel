package fswatch

import (
	"os"
	"time"
)

// ChangedAt is when the file at path last changed, its contents or its name (its ctime), as precisely as its
// filesystem keeps it; false when it can't be told: gone, or kept to the second only, too coarse to order against
// a read.
func ChangedAt(path string) (time.Time, bool) {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}, false
	}
	at := changedAt(fi)
	return at, at.Nanosecond() != 0
}
