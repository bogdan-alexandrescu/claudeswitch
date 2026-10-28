#!/bin/sh
# Draw the app's screens to PNGs, light and dark, for each fixture set:
#
#   base         the test fixtures (macos/Tests/ClaudeSwitchCoreTests/Fixtures)
#   many         RenderFixtures/many over them: five profiles, long names
#   one-profile  RenderFixtures/one-profile over them: no [[profile]] blocks
#   hero         RenderFixtures/hero over them: two healthy profiles (README)
#   tutorial     RenderFixtures/tutorial over them: the CLI screenshots' story,
#                with the Chrome sign-in notice (README app section, TUTORIAL)
#   pools        RenderFixtures/pools over them: shared pools, an empty pool,
#                an account in no pool and one in use by another profile
#   older-cli    RenderFixtures/older-cli over them: a binary from before pools
#   states       RenderFixtures/states over them: a stale daemon, a hard pin,
#                already on the best, a pool running dry, a lifted pin
#   min          the many set, Settings only, at the least the window and its
#                sidebar can be (760 x 520, sidebar 182 pt)
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
SETS="${*:-base many one-profile hero tutorial pools older-cli states min}"
FIXED_NOW="2026-10-08T02:46:41Z"
# The release the renders show, as plugin.json (bumped every release) says.
VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$HERE/../plugin/.claude-plugin/plugin.json")"

swift build --package-path "$HERE" >/dev/null
BIN="$(swift build --package-path "$HERE" --show-bin-path)/ClaudeSwitchBar"

for set in $SETS; do
  work="$(mktemp -d)"
  cp "$HERE/Tests/ClaudeSwitchCoreTests/Fixtures/"*.json "$work/"
  now=""
  fx="$set" least=""
  if [ "$set" = min ]; then fx=many least="--min"; fi
  if [ "$fx" != base ]; then
    # *.jsonl too: a set may bring audit log lines (render-audit.jsonl).
    cp "$HERE/RenderFixtures/$fx/"*.json "$HERE/RenderFixtures/$fx/"*.jsonl "$work/" 2>/dev/null || true
    now="--now $FIXED_NOW"
  fi
  mkdir -p "$OUT/$set"
  # shellcheck disable=SC2086 # $now and $least are empty or whole words
  "$BIN" --render "$work" "$OUT/$set" $now --version "$VERSION" $least
  rm -rf "$work"
  echo "$OUT/$set"
done
