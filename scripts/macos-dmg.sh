#!/bin/sh
# Packs Oriel.app into a DMG for first install, beside an /Applications link to drag it onto: make dmg runs it.
#
#	scripts/macos-dmg.sh APP DMG
set -eu

if [ "$(uname -s)" != Darwin ]; then
	echo "macos-dmg.sh: making a DMG needs macOS (hdiutil)" >&2
	exit 1
fi
if [ $# -ne 2 ]; then
	echo "usage: $0 APP DMG" >&2
	exit 2
fi
app=$1 dmg=$2

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
ditto "$app" "$stage/$(basename "$app")"
ln -s /Applications "$stage/Applications"
rm -f "$dmg"
hdiutil create -volname Oriel -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dmg"
echo "made $dmg"
