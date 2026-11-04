#!/usr/bin/env bash
# Builds each grammar in manifest.tsv, with the tree-sitter runtime and shim.c, into <name>.wasm.gz, from
# sources fetched at their pinned revisions and checked against their sha256. Needs wasi-sdk 34 at
# $WASI_SDK_PATH (default /opt/wasi-sdk). SHA256SUMS is of the uncompressed .wasm; CI rebuilds and checks it
# matches (gzip's own bytes vary with its version, so they aren't compared).
#
# Then each grammar pack in packs.tsv: <name>.tar.gz in $PACKS_OUT (default packs-out, not committed),
# holding <name>/language.toml (from packs/), grammar.wasm, and the queries at the pinned nvim-treesitter commit
# with its licence. PACKS.SHA256SUMS lists every file of every pack; the binary embeds it and installs only packs
# that match. "build.sh packs" builds the packs alone.
set -euo pipefail
export LC_ALL=C # globs and sorts in byte order, whatever the machine's locale
cd "$(dirname "$0")"
sdk=${WASI_SDK_PATH:-/opt/wasi-sdk}
out=${PACKS_OUT:-packs-out}
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

compile() { # name grammar-directory runtime-directory output
	local files=("$3/src/lib.c" "$2/src/parser.c" shim.c)
	[ -f "$2/src/scanner.c" ] && files+=("$2/src/scanner.c")
	"$sdk/bin/clang" --target=wasm32-wasip1 -O2 -mexec-model=reactor -std=c11 -D_DEFAULT_SOURCE -DNDEBUG -ffile-prefix-map="$work"=. \
		-DLANGUAGE_FN="tree_sitter_$1" -I"$3/include" -I"$3/src" -I"$2/src" \
		-Wno-unused-parameter -Wno-unused-but-set-variable \
		-o "$4" "${files[@]}"
}

licence() { # repo source-directory: copies its licence to licenses/<owner>_<repo>.txt
	local f=("$2"/LICENSE*)
	cp "${f[0]}" "licenses/$(echo "$1" | tr / _).txt"
}

IFS=$'\t' read -r _ repo rev sub sum _ < <(grep '^runtime	' manifest.tsv)
rtsrc=$(fetch "$repo" "$rev" "$sum")
rt="$rtsrc/$sub"

if [ "${1:-}" != packs ]; then
	mkdir -p licenses
	licence "$repo" "$rtsrc"
	rtlic="licenses/$(echo "$repo" | tr / _).txt"
	printf '\n%s\nlib/src/unicode/LICENSE:\n\n' "$(printf -- '-%.0s' $(seq 72))" >>"$rtlic"
	cat "$rt/src/unicode/LICENSE" >>"$rtlic"
	grep -v '^#' manifest.tsv | grep -v '^runtime	' | while IFS=$'\t' read -r name repo rev sub sum _; do
		src=$(fetch "$repo" "$rev" "$sum")
		licence "$repo" "$src"
		compile "$name" "$src/$sub" "$rt" "$work/$name.wasm"
		gzip -9 -n -c "$work/$name.wasm" >"$name.wasm.gz"
		echo "built $name.wasm.gz"
	done
	(cd "$work" && sha256 *.wasm) >SHA256SUMS
fi

IFS=$'\t' read -r _ repo rev sub sum < <(grep '^queries	' packs.tsv)
qroot=$(fetch "$repo" "$rev" "$sum")
queries="$qroot/$sub"
mkdir -p "$work/packs" "$out"
grep -v '^#' packs.tsv | grep -v '^queries	' | while IFS=$'\t' read -r name repo rev sub sum; do
	src=$(fetch "$repo" "$rev" "$sum")
	pack="$work/packs/$name"
	mkdir -p "$pack/queries"
	compile "$name" "$src/$sub" "$rt" "$pack/grammar.wasm"
	cp "packs/$name.toml" "$pack/language.toml"
	for kind in highlights folds injections; do
		[ -f "$queries/$name/$kind.scm" ] && cp "$queries/$name/$kind.scm" "$pack/queries/"
	done
	cp "$qroot/LICENSE" "$pack/queries/LICENSE"
	tar -czf "$out/$name.tar.gz" -C "$work/packs" "$name"
	echo "built pack $name.tar.gz"
done
(cd "$work/packs" && find . -type f | sed 's|^\./||' | LC_ALL=C sort | while read -r f; do sha256 "$f"; done) >PACKS.SHA256SUMS
