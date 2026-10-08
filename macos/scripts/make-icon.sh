#!/bin/sh
# Render the app icon from the Twin rings mark and pack it as an .icns.
#
#   macos/scripts/make-icon.sh [output.icns]   # default: macos/Resources/AppIcon.icns
#
# It builds the app and runs `ClaudeSwitchBar --render-icon <dir.iconset>`,
# which draws every iconset size (16 to 512 pt, @1x and @2x) with
# CoreGraphics from the same TwinRings code as the menu-bar glyph: the mark
# on a dark radial graphite tile, frost unlit dots, glacier week, meltwater
# session. iconutil then packs the set. build-app.sh copies the result into
# the bundle; rerun this after changing the mark and commit AppIcon.icns.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"   # macos/
OUT="${1:-$HERE/Resources/AppIcon.icns}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

swift build --package-path "$HERE" >&2
BIN="$(swift build --package-path "$HERE" --show-bin-path)/ClaudeSwitchBar"

"$BIN" --render-icon "$WORK/AppIcon.iconset"
iconutil -c icns "$WORK/AppIcon.iconset" -o "$OUT"
echo "$OUT"
