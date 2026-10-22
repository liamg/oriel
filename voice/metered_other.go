//go:build !linux && !(darwin && cgo)

package voice

// Metered reports whether the OS counts the network as metered; here it can't say.
func Metered() bool { return false }
