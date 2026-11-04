module github.com/liamg/oriel

go 1.26.0

require (
	gioui.org v0.10.3
	github.com/go-text/typesetting v0.3.5
	github.com/godbus/dbus/v5 v5.2.2
	github.com/tetratelabs/wazero v1.12.0
	github.com/yuin/gopher-lua v1.1.1
	golang.org/x/image v0.47.0
	golang.org/x/sys v0.49.0
)

require (
	gioui.org/shader v1.0.9 // indirect
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// wazero with its file cache reads buffered: third_party/wazero/PATCHED.md.
replace github.com/tetratelabs/wazero => ./third_party/wazero
