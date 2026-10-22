#!/bin/sh
# Packs the Linux release archive, oriel-<version>-linux-<arch>.tar.gz: the binary, its .desktop file, the icon,
# LICENSE and THIRD_PARTY_NOTICES, in one top directory of the same name. release.yml runs it after make build.
#
#	scripts/linux-archive.sh OUTDIR EXE ICON VERSION ARCH
set -eu

if [ $# -ne 5 ]; then
	echo "usage: $0 OUTDIR EXE ICON VERSION ARCH" >&2
	exit 2
fi
out=$1 exe=$2 icon=$3 version=$4 arch=$5
here=$(cd "$(dirname "$0")" && pwd)
root=$(dirname "$here")
name="oriel-${version#v}-linux-$arch"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/$name"
install -m 0755 "$exe" "$work/$name/oriel"
install -m 0644 "$here/oriel.desktop" "$work/$name/oriel.desktop"
install -m 0644 "$icon" "$work/$name/oriel.png"
install -m 0644 "$root/LICENSE" "$root/THIRD_PARTY_NOTICES" "$work/$name/"
mkdir -p "$out"
tar --sort=name --owner=0 --group=0 --numeric-owner -C "$work" -czf "$out/$name.tar.gz" "$name"
echo "packed $out/$name.tar.gz"
