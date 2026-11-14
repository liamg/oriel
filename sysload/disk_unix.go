//go:build unix

package sysload

import "golang.org/x/sys/unix"

// Disk is the space on the volume dir is on: free to this user, and all of it, in bytes.
func Disk(dir string) (free, total uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), uint64(st.Blocks) * uint64(st.Bsize), nil
}
