package fswatch

import (
	"slices"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A watched folder's own deletion, and its own move, read as that folder removed; a file deleted in it, as the file
// removed; and IN_IGNORED, sent once a watch is gone, drops the watch and reads as nothing.
//
// ref: inotify/inotify.7
// quote: Watched file/directory was itself deleted.
func TestInotifyReadsAWatchedFolderGoing(t *testing.T) {
	b := &inotify{wds: map[int32]string{1: "/w/repo"}, paths: map[string]int32{"/w/repo": 1}}
	record := func(mask uint32, name string) []byte {
		n := 0
		if name != "" {
			n = (len(name) + 1 + 15) &^ 15
		}
		buf := make([]byte, unix.SizeofInotifyEvent+n)
		ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[0]))
		ev.Wd, ev.Mask, ev.Len = 1, mask, uint32(n)
		copy(buf[unix.SizeofInotifyEvent:], name)
		return buf
	}
	buf := slices.Concat(record(unix.IN_DELETE, "a.txt"), record(unix.IN_DELETE_SELF, ""), record(unix.IN_IGNORED, ""))
	got := b.parse(buf)
	want := []raw{{path: "/w/repo/a.txt", op: opRemoved}, {path: "/w/repo", dir: true, op: opRemoved}}
	if !slices.Equal(got, want) {
		t.Errorf("parse = %+v, want %+v", got, want)
	}
	if _, ok := b.wds[1]; ok {
		t.Error("IN_IGNORED left the watch in place")
	}
	b = &inotify{wds: map[int32]string{1: "/w/repo"}, paths: map[string]int32{"/w/repo": 1}}
	if got := b.parse(record(unix.IN_MOVE_SELF, "")); !slices.Equal(got, []raw{{path: "/w/repo", dir: true, op: opRemoved}}) {
		t.Errorf("IN_MOVE_SELF = %+v, want the folder removed", got)
	}
}
