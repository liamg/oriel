#!/usr/bin/env bash
# fetch.sh DEST URL SHA256 puts the file URL serves at DEST, checked against SHA256. Downloads are kept in
# $DOWNLOADS (default ~/.cache/oriel-ci), named by their checksum, which the workflows restore with actions/cache:
# a file cached and still matching is used as it is, and only a miss reaches URL, retrying on any error.
set -euo pipefail
dest=$1 url=$2 sum=$3
cache=${DOWNLOADS:-$HOME/.cache/oriel-ci}
mkdir -p "$cache"
kept="$cache/$sum"
check() { echo "$sum  $1" | shasum -a 256 -c - >/dev/null; }
if [ -f "$kept" ] && check "$kept"; then
  echo "fetch: $(basename "$dest") from the cache"
else
  rm -f "$kept"
  curl -sSfL --retry 5 --retry-all-errors --retry-delay 5 -o "$kept.part" "$url"
  check "$kept.part" || { echo "fetch: $url doesn't match $sum" >&2; rm -f "$kept.part"; exit 1; }
  mv "$kept.part" "$kept"
fi
cp "$kept" "$dest"
