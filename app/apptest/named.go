package apptest

import "github.com/liamg/oriel/core"

// Named is the id of the session named name, the latest started if several are; "" if none is. A session's id is its
// own, random, apart from its name.
func Named(st core.State, name string) core.SessionID {
	var id core.SessionID
	for _, se := range st.Sessions {
		if se.Name == name {
			id = se.ID
		}
	}
	return id
}

// FocusedOn reports whether the session named name is the one in front.
func FocusedOn(st core.State, name string) bool {
	id := Named(st, name)
	return id != "" && st.Focus == id
}
