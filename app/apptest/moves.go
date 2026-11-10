package apptest

// Follow waits for the window to be asked to open on another directory, as focusing a session there asks it, and
// moves it there (Reopen): the ask is handed to the loop, so a Reopen straight after the focus can come too soon.
func (s *Scene) Follow() {
	s.T.Helper()
	if s.Moves() {
		return
	}
	s.Until("the window asked to open on another directory", s.ReopenAsked)
	s.Reopen()
}

// Moves runs what the loop was handed, moving the window wherever that asks it to open (Reopen) before any frame, as
// the loop takes a move before it draws again; then it settles (Quiet). It says whether the window moved.
func (s *Scene) Moves() bool {
	s.T.Helper()
	moved := false
	for s.Queued() || s.ReopenAsked() {
		s.RunQueued()
		moved = s.Reopen() || moved
	}
	s.Quiet()
	return moved
}
