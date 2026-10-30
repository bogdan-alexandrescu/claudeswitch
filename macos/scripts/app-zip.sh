#!/bin/sh
# Build a universal ClaudeSwitch.app and zip it for attaching to a GitHub
# release.
#
#   macos/scripts/app-zip.sh [output-dir]       # default: macos/dist
#
# By default the app is ad-hoc signed. Without a Developer ID signature and
# notarization, a downloaded copy is quarantined and Gatekeeper refuses its
# first launch; macos/README.md has the one-time "Open Anyway" steps, and
# building with ./install-app.sh avoids them.
#
# With SIGN_IDENTITY set (see build-app.sh) the app is signed with that
# Developer ID, then notarized (with the credentials notarize.sh reads) and
# stapled before it is zipped.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"
DIST="${1:-$HERE/dist}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

SIGNED=
if [ -n "${SIGN_IDENTITY:-}" ] && [ "$SIGN_IDENTITY" != - ]; then
  SIGNED=1
  # Checked before the build: a Developer ID app that is not notarized is
  # blocked just like an unsigned one, so it is never worth shipping.
  "$HERE/scripts/notarize.sh" --check
fi

APP="$(UNIVERSAL=1 "$HERE/scripts/build-app.sh" "$WORK")"
VERSION="$(/usr/libexec/PlistBuddy -c 'Print CFBundleShortVersionString' "$APP/Contents/Info.plist")"

if [ -n "$SIGNED" ]; then
  # Apple notarizes a zip of the app, and the ticket is stapled to the app
  # itself, so the zip is made twice: once to submit, once to ship.
  ditto -c -k --keepParent "$APP" "$WORK/submit.zip"
  "$HERE/scripts/notarize.sh" "$WORK/submit.zip"
  xcrun stapler staple "$APP" >&2
  xcrun stapler validate "$APP" >&2
  # What Gatekeeper will say to a downloaded copy: "Notarized Developer ID".
  spctl --assess --type execute --verbose=2 "$APP" >&2
fi

mkdir -p "$DIST"
ZIP="$DIST/ClaudeSwitch-$VERSION-macos.zip"
rm -f "$ZIP"
# ditto keeps the bundle's symlinks, permissions and signature intact; zip -r
# does not reliably.
ditto -c -k --sequesterRsrc --keepParent "$APP" "$ZIP"
shasum -a 256 "$ZIP" | sed "s#  $DIST/#  #" > "$ZIP.sha256"
echo "$ZIP"
cat "$ZIP.sha256"
