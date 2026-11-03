//go:build !linux && !(darwin && !ios)

package osclip

func files() []string { return nil }
func image() []byte   { return nil }
