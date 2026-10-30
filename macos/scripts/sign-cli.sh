#!/bin/sh
# Sign claudeswitch's macOS binaries with the Developer ID and notarize them,
# for a release's darwin tarballs.
#
#   SIGN_IDENTITY="Developer ID Application: …" macos/scripts/sign-cli.sh <binary>...
#
# SIGN_KEYCHAIN names the keychain that holds the identity, if not the
# default; notarizing needs the credentials notarize.sh reads. A bare binary
# cannot carry a stapled ticket: Gatekeeper looks it up online the first time
# a quarantined copy runs.
set -eu

HERE="$(cd "$(dirname "$0")/.." && pwd)"   # macos/
[ $# -gt 0 ] || { echo "usage: sign-cli.sh <binary>..." >&2; exit 2; }
: "${SIGN_IDENTITY:?SIGN_IDENTITY is not set}"
"$HERE/scripts/notarize.sh" --check

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir "$WORK/submit"
n=0
for bin in "$@"; do
  codesign --force --options runtime --timestamp --identifier xyz.claudeswitch.cli \
    ${SIGN_KEYCHAIN:+--keychain "$SIGN_KEYCHAIN"} --sign "$SIGN_IDENTITY" "$bin" >&2
  codesign --verify --strict "$bin" >&2
  # One submission for them all, each under a name of its own.
  n=$((n + 1))
  cp "$bin" "$WORK/submit/claudeswitch-$n"
done
ditto -c -k "$WORK/submit" "$WORK/submit.zip"
"$HERE/scripts/notarize.sh" "$WORK/submit.zip"
