#!/bin/sh
# Build ClaudeSwitch.app from source with the Command Line Tools alone.
#
#   macos/scripts/build-app.sh [output-dir]      # default: macos/build
#   UNIVERSAL=1 macos/scripts/build-app.sh       # arm64 + x86_64, for a release zip
#
# SwiftPM compiles the executable; this wraps it in a bundle with Info.plist
# (LSUIElement, so no Dock icon) and signs it ad hoc. An ad-hoc signature is
# not an identity: it only satisfies Apple silicon's requirement that every
# binary be signed. No Xcode project and no Apple Developer account involved.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"   # macos/
ROOT="$(cd "$HERE/.." && pwd)"
OUT="${1:-$HERE/build}"
APP="$OUT/ClaudeSwitch.app"

[ "$(uname -s)" = Darwin ] || { echo "the menu-bar app is macOS only" >&2; exit 1; }
if ! command -v swift >/dev/null 2>&1 || ! swift --version >/dev/null 2>&1; then
  echo "Swift is not available. Install the Command Line Tools first:" >&2
  echo "  xcode-select --install" >&2
  exit 1
fi

# The app is versioned with the binary: the plugin manifest carries the
# release number, bumped with every tag.
VERSION="$(sed -n 's/.*"version": *"\([0-9][0-9.]*\)".*/\1/p' "$ROOT/plugin/.claude-plugin/plugin.json" 2>/dev/null | head -1)"
VERSION="${VERSION:-0.0.0}"
BUILD="$(git -C "$ROOT" rev-list --count HEAD 2>/dev/null || echo 1)"

build() { # build <extra swift build args...>
  swift build -c release --package-path "$HERE" "$@" >&2
  swift build -c release --package-path "$HERE" "$@" --show-bin-path
}

echo "building ClaudeSwitch $VERSION ($BUILD)..." >&2
if [ "${UNIVERSAL:-}" = 1 ]; then
  # One triple at a time and lipo: a multi --arch build needs full Xcode.
  ARM="$(build --triple arm64-apple-macosx13.0)/ClaudeSwitchBar"
  X86="$(build --triple x86_64-apple-macosx13.0)/ClaudeSwitchBar"
  BIN="$(mktemp -d)/ClaudeSwitchBar"
  lipo -create "$ARM" "$X86" -output "$BIN"
else
  BIN="$(build)/ClaudeSwitchBar"
fi

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/ClaudeSwitch"
sed -e "s/__VERSION__/$VERSION/" -e "s/__BUILD__/$BUILD/" \
  "$HERE/Resources/Info.plist" > "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"

codesign --force --sign - --timestamp=none "$APP" >&2
codesign --verify "$APP" >&2

echo "$APP"
