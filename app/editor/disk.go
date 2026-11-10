package editor

import (
	"os"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// diskWatch is how the editor notices files changing under it.
type diskWatch struct {
	next     time.Time
	asked    map[int]time.Time // buffers whose change on disk was asked about, with the change's time
	checking bool              // a look at the files is running, off the loop
}

// diskChange is an open buffer whose file changed on disk, as a look off the loop found it: read again when the
// buffer had no changes of its own.
type diskChange struct {
	num      int
	name     string
	file     *edit.File // the buffer's file when it was looked at
	modified bool
	mod      time.Time
	data     []byte
	read     *edit.File
	err      error
}

// diskInterval is how often the files of open buffers are looked at.
const diskInterval = time.Second

// checkDisk looks at the open buffers' files while a pane shows one, and wakes the frame for the next look.
func (x *Editor) checkDisk(c *ui.Ctx) {
	c.WakeAt(x.lookAtDisk(c.Now()))
}

// lookAtDisk looks at the open buffers' files off the loop, at most every diskInterval, then reloads a buffer whose
// file changed on disk, or asks when the buffer has changes of its own. It runs while a pane shows a buffer and
// whenever another view reads one through the Editors service (a diff of a file open behind it), so no view
// shows a buffer's text from before its file changed. It answers when the next look is due. On the loop.
func (x *Editor) lookAtDisk(now time.Time) time.Time {
	if now.Before(x.disk.next) {
		return x.disk.next
	}
	x.disk.next = now.Add(diskInterval)
	if !x.disk.checking {
		x.startDiskLook()
	}
	return x.disk.next
}

// startDiskLook starts a look at the open buffers' files off the loop.
func (x *Editor) startDiskLook() {
	var looks []diskChange
	for _, f := range x.e.Files() {
		looks = append(looks, diskChange{num: f.Num, name: f.Name, file: f.File, modified: f.Modified})
	}
	if len(looks) == 0 {
		return
	}
	files := make([]edit.File, len(looks))
	for i, l := range looks {
		files[i] = *l.file
	}
	x.disk.checking = true
	disk := x.fileHost.disk
	x.busy.Go(func() {
		var found []diskChange
		for i, l := range looks {
			if changed, err := files[i].ChangedOnDisk(); err != nil || !changed {
				continue
			}
			if st, err := os.Stat(files[i].Path); err == nil {
				l.mod = st.ModTime()
			}
			if !l.modified {
				l.data, l.read, l.err = disk.ReadFile(l.name)
			}
			found = append(found, l)
		}
		x.h.Do(func() {
			x.disk.checking = false
			x.madeMu.Lock()
			stopped := x.stopped
			x.madeMu.Unlock()
			if !stopped {
				x.changedOnDisk(found)
			}
		})
	})
}

// changedOnDisk reloads or asks about the buffers a look off the loop found changed, unless they've been saved or
// read again since.
func (x *Editor) changedOnDisk(found []diskChange) {
	now := map[int]vim.FileState{}
	for _, f := range x.e.Files() {
		now[f.Num] = f
	}
	for _, ch := range found {
		f, ok := now[ch.num]
		if !ok || f.File != ch.file {
			continue
		}
		if !f.Modified {
			if ch.modified {
				continue
			}
			data, read, err := x.fileHost.opened(ch.name, ch.data, ch.read, ch.err)
			if err == nil {
				err = x.e.ReloadBufferFrom(ch.num, data, read)
			}
			if err != nil {
				x.h.Notify(core.Notice{ID: "editor.reload", Severity: core.SevError, Source: "editor", Text: err.Error()})
			}
			continue
		}
		if t, ok := x.disk.asked[f.Num]; ok && t.Equal(ch.mod) {
			continue
		}
		x.disk.asked[f.Num] = ch.mod
		x.h.Notify(core.Notice{ID: "editor.changed-on-disk." + f.Name, Severity: core.SevQuestion, Source: "editor", Title: "Changed on disk",
			Text: f.Name + " changed on disk, and has unsaved changes here.",
			Actions: []core.Action{
				{Title: "Reload", Command: "editor.reload", Args: map[string]any{"buffer": f.Num}},
				{Title: "Keep mine", Command: "editor.keep", Args: map[string]any{"buffer": f.Num}},
			}})
	}
}
