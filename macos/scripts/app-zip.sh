#!/bin/sh
# Build a universal, unsigned (ad-hoc signed) ClaudeSwitch.app and zip it for
# attaching to a GitHub release.
#
#   macos/scripts/app-zip.sh [output-dir]       # default: macos/dist
#
# Without a Developer ID signature and notarization, a downloaded copy is
# quarantined and Gatekeeper refuses its first launch. macos/README.md has the
# one-time "Open Anyway" steps; building with ./install-app.sh avoids them.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"
DIST="${1:-$HERE/dist}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

APP="$(UNIVERSAL=1 "$HERE/scripts/build-app.sh" "$WORK")"
VERSION="$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$APP/Contents/Info.plist")"

mkdir -p "$DIST"
ZIP="$DIST/ClaudeSwitch-$VERSION-macos.zip"
rm -f "$ZIP"
# ditto keeps the bundle's symlinks, permissions and signature intact; zip -r
# does not reliably.
ditto -c -k --sequesterRsrc --keepParent "$APP" "$ZIP"
shasum -a 256 "$ZIP" | sed "s#  $DIST/#  #" > "$ZIP.sha256"
echo "$ZIP"
cat "$ZIP.sha256"
