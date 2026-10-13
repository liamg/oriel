#!/usr/bin/env bash
# Builds oriel-vt.wasm.gz: libghostty-vt at the pinned Ghostty commit, with shim.c, for wasm32. Needs zig 0.16.0
# (what that commit's build.zig.zon asks for) as $ZIG or on PATH. SHA256SUMS is of the uncompressed .wasm; CI rebuilds
# and checks it matches.
set -euo pipefail
cd "$(dirname "$0")"
ghostty=80456f05732a68b3fed99eb7396eddf8b7981080
ghostty_sum=9462781e9d67c0dc446a5391a989e48ca1b722a91440b1a1e90fc86ca5498c91
zig_version=0.16.0
zig=${ZIG:-zig}
[ "$("$zig" version)" = "$zig_version" ] || { echo "need zig $zig_version" >&2; exit 1; }
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
export ZIG_GLOBAL_CACHE_DIR=${ZIG_GLOBAL_CACHE_DIR:-$work/cache}

curl -fsSL "https://codeload.github.com/ghostty-org/ghostty/tar.gz/$ghostty" -o "$work/ghostty.tgz"
echo "$ghostty_sum  $work/ghostty.tgz" | sha256 -c - >/dev/null
mkdir "$work/src" && tar -xzf "$work/ghostty.tgz" -C "$work/src" --strip-components=1

# The packages the lib-vt build reads, fetched with curl (which honours proxies) into zig's cache. build.zig.zon pins
# each by hash, so a changed download isn't used.
for url in \
	https://github.com/jacobsandlund/uucode/archive/9d55524551411b493cca41ca06363625d90aff1e.tar.gz \
	https://codeberg.org/vancluever/translate-c/archive/4e879eb8aba615de112eabd1231ea6e01920cead.tar.gz \
	https://github.com/vancluever/arocc/archive/f97cdfc3779aec4b242299e2fc9a1c828c3547c6.tar.gz \
	https://deps.files.ghostty.org/ghostty-themes-release-20260921-150923-0b55a9e.tgz \
	https://deps.files.ghostty.org/zlib-1220fed0c74e1019b3ee29edae2051788b080cd96e90d56836eea857b0b966742efb.tar.gz; do
	f="$work/dep-$(basename "$url")"
	curl -fsSL "$url" -o "$f"
	(cd "$work/src" && "$zig" fetch "$f" >/dev/null)
done

(cd "$work/src" && "$zig" build -Demit-lib-vt -Dtarget=wasm32-freestanding -Doptimize=ReleaseFast)

exports=()
for f in $(grep -oE '^[a-zA-Z].*\b(oriel_[a-z_]+)\(' shim.c | grep -oE 'oriel_[a-z_]+'); do exports+=("-Wl,--export=$f"); done
"$zig" cc -target wasm32-freestanding -O2 -I"$work/src/include" -ffile-prefix-map="$work"=. -c shim.c -o "$work/shim.o"
"$zig" cc -target wasm32-freestanding -O2 -nostdlib -Wl,--no-entry -Wl,--strip-all -Wl,-z,stack-size=131072 \
	"${exports[@]}" "$work/shim.o" "$work/src/zig-out/lib/libghostty-vt.a" -o "$work/oriel-vt.wasm"
gzip -9 -n -c "$work/oriel-vt.wasm" >oriel-vt.wasm.gz
(cd "$work" && sha256 oriel-vt.wasm) >SHA256SUMS
echo "built oriel-vt.wasm.gz"
