# Sourced by wasm/build.sh and native/build.sh: fetches Ghostty at its pinned commit, checked against its sha256, into
# $work/src, with the packages its lib-vt build reads in zig's cache. Needs $work, $zig and sha256.
ghostty=80456f05732a68b3fed99eb7396eddf8b7981080
ghostty_sum=9462781e9d67c0dc446a5391a989e48ca1b722a91440b1a1e90fc86ca5498c91
zig_version=0.16.0
zig=${ZIG:-zig}
[ "$("$zig" version)" = "$zig_version" ] || { echo "need zig $zig_version" >&2; exit 1; }
# A fixed path, not a random one: the paths the build sees end up in the objects, so the output stays byte for byte.
work=/tmp/oriel-ghostty-build
rm -rf "$work" && mkdir -p "$work"
trap 'rm -rf "$work"' EXIT
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
export ZIG_GLOBAL_CACHE_DIR=${ZIG_GLOBAL_CACHE_DIR:-$work/cache}

if [ -n "${GHOSTTY_TGZ:-}" ]; then
	cp "$GHOSTTY_TGZ" "$work/ghostty.tgz" # a copy already downloaded; still checked below
else
	curl -fsSL "https://codeload.github.com/ghostty-org/ghostty/tar.gz/$ghostty" -o "$work/ghostty.tgz"
fi
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


# zig_build runs zig build in the source, fetching with curl any package it says it couldn't download (zig's own
# fetch doesn't go through a proxy), and trying again. Each package is still checked against the hash its
# build.zig.zon pins.
zig_build() {
	for _ in $(seq 40); do
		if out=$(cd "$work/src" && "$zig" build "$@" 2>&1); then
			return 0
		fi
		url=$(printf '%s\n' "$out" | grep -A1 'bad HTTP response code\|unable to fetch\|error: .*connection' | grep -oE '"https?://[^"]+"' | head -1 | tr -d '"')
		if [ -z "$url" ]; then
			printf '%s\n' "$out" >&2
			return 1
		fi
		f="$work/dep-$(basename "$url")"
		curl -fsSL "$url" -o "$f"
		(cd "$work/src" && "$zig" fetch "$f" >/dev/null)
	done
	return 1
}
