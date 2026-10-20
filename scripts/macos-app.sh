#!/bin/sh
# Assembles and signs Oriel.app (distribution.md): make app runs it after make build.
#
#	scripts/macos-app.sh APP EXE ICNS VERSION IDENTITY
#
# IDENTITY is a codesign identity: "-" signs ad hoc; release.yml passes the self-signed one.
set -eu

if [ "$(uname -s)" != Darwin ]; then
	echo "macos-app.sh: building Oriel.app needs macOS (codesign)" >&2
	exit 1
fi
if [ $# -ne 5 ]; then
	echo "usage: $0 APP EXE ICNS VERSION IDENTITY" >&2
	exit 2
fi
app=$1 exe=$2 icns=$3 version=$4 identity=$5
here=$(cd "$(dirname "$0")" && pwd)

# Bundle versions are up to three dot-separated integers: v0.5.0-rc.1 is 0.5.0, and a dev build 0.0.0.
short=${version#v}
short=${short%%-*}
case "$short" in
[0-9]*) ;;
*) short=0.0.0 ;;
esac

rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$exe" "$app/Contents/MacOS/oriel"
cp "$icns" "$app/Contents/Resources/oriel.icns"
cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>io.github.liamg.oriel</string>
	<key>CFBundleName</key>
	<string>Oriel</string>
	<key>CFBundleDisplayName</key>
	<string>Oriel</string>
	<key>CFBundleExecutable</key>
	<string>oriel</string>
	<key>CFBundleIconFile</key>
	<string>oriel.icns</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleShortVersionString</key>
	<string>$short</string>
	<key>CFBundleVersion</key>
	<string>$short</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSMicrophoneUsageDescription</key>
	<string>Oriel listens to the microphone while you dictate.</string>
</dict>
</plist>
PLIST
plutil -lint "$app/Contents/Info.plist" >/dev/null

codesign --force --deep --options runtime --entitlements "$here/oriel.entitlements" --sign "$identity" "$app"
codesign --verify --deep --strict "$app"
echo "signed $app ($identity)"
