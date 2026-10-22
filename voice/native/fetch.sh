# Sourced by build.sh: fetches whisper.cpp at its pinned release, checked against its sha256, into $work/src.
whisper=d1be6fde11ac6e0407606b4e42fe72d34add8037 # v1.9.5
whisper_sum=645445da884df6dbd52b30f82d7be7c01d1767c938714e477e786b88d529e9f4
# A fixed path, not a random one: the paths the build sees end up in the objects.
work=/tmp/oriel-whisper-build
rm -rf "$work" && mkdir -p "$work"
trap 'rm -rf "$work"' EXIT
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

if [ -n "${WHISPER_TGZ:-}" ]; then
	cp "$WHISPER_TGZ" "$work/whisper.tgz" # a copy already downloaded; still checked below
else
	curl -fsSL "https://codeload.github.com/ggml-org/whisper.cpp/tar.gz/$whisper" -o "$work/whisper.tgz"
fi
echo "$whisper_sum  $work/whisper.tgz" | sha256 -c - >/dev/null
mkdir "$work/src" && tar -xzf "$work/whisper.tgz" -C "$work/src" --strip-components=1
