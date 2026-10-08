#!/bin/sh
# Draw the app's screens to PNGs, light and dark, for each fixture set:
#
#   base         the test fixtures (macos/Tests/ClaudeSwitchCoreTests/Fixtures)
#   many         RenderFixtures/many over them: five profiles, long names
#   one-profile  RenderFixtures/one-profile over them: no [[profile]] blocks
#   hero         RenderFixtures/hero over them: two healthy profiles (README)
#   tutorial     RenderFixtures/tutorial over them: the CLI screenshots' story,
#                with the Chrome sign-in notice (README app section, TUTORIAL)
#
#   macos/scripts/render.sh <out-dir> [set...]
#
# It builds the app and runs `ClaudeSwitchBar --render`, which reads only the
# JSON it is given and runs no command (no keychain, no daemon, no CLI).
# The overlay sets are written at a fixed clock, passed as --now.
# Regenerate them with macos/scripts/gen-render-fixtures.py.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${1:?usage: render.sh <out-dir> [set...]}"
shift
SETS="${*:-base many one-profile hero tutorial}"
FIXED_NOW="2026-10-08T02:46:41Z"

swift build --package-path "$HERE" >/dev/null
BIN="$(swift build --package-path "$HERE" --show-bin-path)/ClaudeSwitchBar"

for set in $SETS; do
  work="$(mktemp -d)"
  cp "$HERE/Tests/ClaudeSwitchCoreTests/Fixtures/"*.json "$work/"
  now=""
  if [ "$set" != base ]; then
    cp "$HERE/RenderFixtures/$set/"*.json "$work/"
    now="--now $FIXED_NOW"
  fi
  mkdir -p "$OUT/$set"
  # shellcheck disable=SC2086 # $now is empty or two words
  "$BIN" --render "$work" "$OUT/$set" $now
  rm -rf "$work"
  echo "$OUT/$set"
done
