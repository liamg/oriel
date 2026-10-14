#!/usr/bin/env bash
# Builds libghostty-vt at the pinned Ghostty commit as a static library for each platform the desktop app ships on,
# into lib/<goos>_<goarch>/, with its headers in include/, for the ghostty_native build tag (cgo). Needs zig 0.16.0 as
# $ZIG or on PATH; zig cross-compiles every target from one machine. The libraries aren't committed: zig embeds its cache paths, so
# they aren't byte-reproducible, and the source they're built from is pinned by hash. Pass targets (linux_amd64…) to
# build only those.
set -euo pipefail
cd "$(dirname "$0")"
. ../fetch.sh

declare -A triples=([linux_amd64]=x86_64-linux-gnu [linux_arm64]=aarch64-linux-gnu [darwin_amd64]=x86_64-macos [darwin_arm64]=aarch64-macos)
targets=("$@")
[ ${#targets[@]} -gt 0 ] || targets=(darwin_amd64 darwin_arm64 linux_amd64 linux_arm64)
rm -rf include && cp -r "$work/src/include" include
for t in "${targets[@]}"; do
	rm -rf "$work/src/zig-out" && zig_build -Demit-lib-vt -Dtarget="${triples[$t]}" -Doptimize=ReleaseFast
	mkdir -p "lib/$t"
	cp "$work/src/zig-out/lib/libghostty-vt.a" "lib/$t/libghostty-vt.a"
	echo "built lib/$t/libghostty-vt.a"
done
