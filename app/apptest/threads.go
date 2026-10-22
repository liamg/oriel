package apptest

import (
	"fmt"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

// ThreadsScene is a session, "notes", on o.WorkDir with open threads anchored in it: each {file, line, text, body}.
// Its clock is at 14:30.
func ThreadsScene(t *testing.T, o Options, threads ...[4]string) *Scene {
	t.Helper()
	s := New(t, o)
	s.Now = At(14, 30, 0)
	dir := o.WorkDir
	se := core.SessionState{ID: "notes", Name: "notes", Kind: core.KindWork, Status: core.StatusWorking,
		Projects: []core.Project{{Name: "notes", Dir: dir, Branch: "main", Primary: true}}}
	for i, th := range threads {
		var line int
		fmt.Sscan(th[1], &line)
		se.Threads = append(se.Threads, core.Thread{ID: core.ThreadID(fmt.Sprintf("t%d00000", i)), Status: core.ThreadOpen, Created: s.Now.Add(-time.Minute),
			Updated: s.Now.Add(-time.Minute), Anchor: &core.Anchor{Project: "notes", File: th[0], Line: line, Text: th[2]},
			Comments: []core.Comment{{ID: fmt.Sprint("c", i), Author: core.AuthorYou, Name: "Liam", Body: th[3], Time: s.Now.Add(-time.Minute), Delivered: true}}})
	}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	return s
}
