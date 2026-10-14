package ghostty

// machine runs one terminal's shim (wasm/shim.c): wasm under wazero, or native through cgo. Calls take the shim's
// arguments after its Term, which the machine holds; oriel_mark_free takes none.
type machine interface {
	call(name string, args ...uint64) uint64
	out(n uint64) []byte // the last call's result, n bytes, valid until the next call
	in(p []byte)         // puts p where the next call reads its input
	replyLen() uint32
	events() uint32
	held() bool
	close() error
}

func newMachine(backend string, cols, rows, scrollback int) (machine, error) {
	if backend == "native" || backend == "" && nativeBuilt {
		return newNative(cols, rows, scrollback)
	}
	return newWasm(cols, rows, scrollback)
}

// Native reports whether the native core is built in.
func Native() bool { return nativeBuilt }
