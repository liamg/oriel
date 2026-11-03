#!/usr/bin/env bash
# Builds oriel-vt.wasm.gz: libghostty-vt at the pinned Ghostty commit, with shim.c, for wasm32. Needs zig 0.16.0
# (what that commit's build.zig.zon asks for) as $ZIG or on PATH. SHA256SUMS is of the uncompressed .wasm; CI rebuilds
# and checks it matches.
set -euo pipefail
cd "$(dirname "$0")"
. ../fetch.sh

zig_build -Demit-lib-vt -Dtarget=wasm32-freestanding -Doptimize=ReleaseFast

exports=()
for f in $(grep -oE '^[a-zA-Z].*\b(oriel_[a-z_]+)\(' shim.c | grep -oE 'oriel_[a-z_]+'); do exports+=("-Wl,--export=$f"); done
"$zig" cc -target wasm32-freestanding -O2 -I"$work/src/include" -ffile-prefix-map="$work"=. -c shim.c -o "$work/shim.o"
"$zig" cc -target wasm32-freestanding -O2 -nostdlib -Wl,--no-entry -Wl,--strip-all -Wl,-z,stack-size=131072 \
	"${exports[@]}" "$work/shim.o" "$work/src/zig-out/lib/libghostty-vt.a" -o "$work/oriel-vt.wasm"
gzip -9 -n -c "$work/oriel-vt.wasm" >oriel-vt.wasm.gz
(cd "$work" && sha256 oriel-vt.wasm) >SHA256SUMS
echo "built oriel-vt.wasm.gz"
