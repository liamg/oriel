#!/usr/bin/env bash
# Builds each grammar in manifest.tsv, with the tree-sitter runtime and shim.c, into <name>.wasm, from
# sources fetched at their pinned revisions and checked against their sha256. Needs wasi-sdk at
# $WASI_SDK_PATH (default /opt/wasi-sdk). Writes SHA256SUMS; CI rebuilds and checks the result matches.
set -euo pipefail
cd "$(dirname "$0")"
sdk=${WASI_SDK_PATH:-/opt/wasi-sdk}
work=$(mktemp -d)
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
trap 'rm -rf "$work"' EXIT

fetch() { # repo revision sha256 -> directory
	local dir="$work/$(echo "$1@$2" | tr / _)"
	if [ ! -d "$dir" ]; then
		curl -fsSL "https://codeload.github.com/$1/tar.gz/$2" -o "$dir.tgz"
		echo "$3  $dir.tgz" | sha256 -c - >/dev/null
		mkdir -p "$dir" && tar -xzf "$dir.tgz" -C "$dir" --strip-components=1
	fi
	echo "$dir"
}

runtime=""
grep -v '^#' manifest.tsv | while IFS=$'\t' read -r name repo rev sub sum; do
	src=$(fetch "$repo" "$rev" "$sum")
	if [ "$name" = runtime ]; then
		echo "$src/$sub" >"$work/runtime"
		continue
	fi
	rt=$(cat "$work/runtime")
	files=("$rt/src/lib.c" "$src/$sub/src/parser.c" shim.c)
	[ -f "$src/$sub/src/scanner.c" ] && files+=("$src/$sub/src/scanner.c")
	"$sdk/bin/clang" --target=wasm32-wasip1 -O2 -mexec-model=reactor -std=c11 -D_DEFAULT_SOURCE -DNDEBUG -ffile-prefix-map="$work"=. \
		-DLANGUAGE_FN="tree_sitter_$name" -I"$rt/include" -I"$rt/src" -I"$src/$sub/src" \
		-Wno-unused-parameter -Wno-unused-but-set-variable \
		-o "$name.wasm" "${files[@]}"
	echo "built $name.wasm"
done
sha256 *.wasm >SHA256SUMS
