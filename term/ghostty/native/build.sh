#!/usr/bin/env bash
# Builds libghostty-vt at the pinned Ghostty commit as a static library for each platform the desktop app ships on,
# into lib/<goos>_<goarch>/, with its headers in include/, for the ghostty_native build tag (cgo). Needs zig 0.16.0 as
# $ZIG or on PATH; zig cross-compiles every target from one machine. The libraries aren't committed: zig embeds its cache paths, so
# they aren't byte-reproducible, and the source they're built from is pinned by hash. Pass targets (linux_amd64…) to
# build only those.
set -euo pipefail
cd "$(dirname "$0")"
. ../fetch.sh

# triple is zig's target for a goos_goarch. A function, not an associative array: macOS's own bash is 3.2.
triple() {
	case "$1" in
	linux_amd64) echo x86_64-linux-gnu ;;
	linux_arm64) echo aarch64-linux-gnu ;;
	darwin_amd64) echo x86_64-macos ;;
	darwin_arm64) echo aarch64-macos ;;
	*) echo "no target $1" >&2; exit 1 ;;
	esac
}
targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=(darwin_amd64 darwin_arm64 linux_amd64 linux_arm64)
rm -rf include && cp -r "$work/src/include" include
for t in "${targets[@]}"; do
	# No xcframework: on a Mac it needs a full Xcode's xcodebuild, and only the static library is linked.
	rm -rf "$work/src/zig-out" && zig_build -Demit-lib-vt -Demit-xcframework=false -Dtarget="$(triple "$t")" -Doptimize=ReleaseFast
	mkdir -p "lib/$t"
	cp "$work/src/zig-out/lib/libghostty-vt.a" "lib/$t/libghostty-vt.a"
	echo "built lib/$t/libghostty-vt.a"
done
