#!/bin/sh
# Run the app's unit tests, with Xcode or with the Command Line Tools alone.
#
# The tests use swift-testing. Xcode's toolchain finds it by itself; the
# Command Line Tools ship it too but SwiftPM does not add its framework
# directory, so it is passed here when that is the active toolchain.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"
DEV="${DEVELOPER_DIR:-$(xcode-select -p 2>/dev/null || true)}"
FW="$DEV/Library/Developer/Frameworks"

if [ -d "$FW/Testing.framework" ] && [ "${DEV#*CommandLineTools}" != "$DEV" ]; then
  exec swift test --package-path "$HERE" \
    -Xswiftc -F -Xswiftc "$FW" -Xlinker -rpath -Xlinker "$FW" \
    -Xlinker -rpath -Xlinker "$DEV/Library/Developer/usr/lib" "$@"
fi
exec swift test --package-path "$HERE" "$@"
