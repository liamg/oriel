package ext

import "sync"

var native struct {
	mu   sync.Mutex
	list []Extension
}

// Register adds a native plugin: a Go package linked in with `oriel build --with`, calling this from its init
// (plugins.md, "Performance and native plugins"). It runs as you, unrestricted.
func Register(e Extension) {
	native.mu.Lock()
	defer native.mu.Unlock()
	native.list = append(native.list, e)
}

// Native are the native plugins registered, in the order they were.
func Native() []Extension {
	native.mu.Lock()
	defer native.mu.Unlock()
	return append([]Extension(nil), native.list...)
}
