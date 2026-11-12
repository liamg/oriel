#!/bin/sh
# build.sh rebuilds Twemoji.ttf: jdecked/twemoji's SVGs (the tag in SOURCE) made into a COLR version 0 font (flat
# colour layers over outlines, which render/typeset draws) by Google's nanoemoji, pinned with hashes in
# requirements.txt. It runs in a virtualenv in a temporary directory and takes some minutes on every core.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
tag=v17.0.3
sha=a0855654b633045ae2337537e77f1bb4361162f7fcd910e613eaab1d6d9c5fca
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$work"
curl -sfL "https://github.com/jdecked/twemoji/archive/refs/tags/$tag.tar.gz" -o twemoji.tar.gz
echo "$sha  twemoji.tar.gz" | sha256sum -c -
tar -xzf twemoji.tar.gz --wildcards '*/assets/svg/*'
mkdir named
# nanoemoji reads the code points from Noto's naming, emoji_u1f468_200d_1f4bb.svg.
for f in */assets/svg/*.svg; do
	b=$(basename "$f" .svg)
	cp "$f" "named/emoji_u$(echo "$b" | tr - _).svg"
done
python3 -m venv venv
venv/bin/pip install --require-hashes -q -r "$here/requirements.txt"
PATH="$work/venv/bin:$PATH" venv/bin/nanoemoji --color_format glyf_colr_0 --family Twemoji --output_file Twemoji.ttf --build_dir build named/*.svg
cp build/Twemoji.ttf "$here/Twemoji.ttf"
