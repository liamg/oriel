package apptest

import (
	"strings"
	"time"

	"github.com/liamg/oriel/app/editor"
)

// WaitExit waits for the app to exit on its own, as quitting does, failing the test if it exited with an error.
func (w *Window) WaitExit() {
	w.T.Helper()
	select {
	case err := <-w.done:
		w.once.Do(func() {})
		if err != nil {
			w.T.Fatalf("the app exited with %v", err)
		}
	case <-time.After(10 * time.Second):
		w.T.Fatal("Oriel didn't quit")
	}
}

// WaitSearch frames until project search has finished and the frame has want, and answers the frame's text.
func (s *Scene) WaitSearch(want string) string {
	s.T.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		PluginOf[*editor.Search](s, "search").Wait()
		s.Frame()
		text := s.Text()
		if strings.Contains(text, want) {
			return text
		}
		if time.Now().After(deadline) {
			s.T.Fatalf("no %q:\n%s", want, text)
		}
	}
}
