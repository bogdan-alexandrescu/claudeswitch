#!/bin/sh
# Submit a zip to Apple's notary service and wait for its verdict.
#
#   macos/scripts/notarize.sh <file.zip>
#   macos/scripts/notarize.sh --check      only check the credentials are set
#
# The credentials, one of:
#
#   NOTARY_PROFILE=<name>                      a profile saved with
#                                              `xcrun notarytool store-credentials`
#   NOTARY_KEY=<AuthKey.p8> NOTARY_KEY_ID=<id> NOTARY_ISSUER=<uuid>
#                                              an App Store Connect API key
#
# On a refusal it prints Apple's log, which names each file at fault. It
# waits up to two hours: a new team's first submissions can sit "In Progress"
# for well over the usual few minutes.
set -eu

ZIP="${1:?usage: notarize.sh <file.zip> | --check}"
if [ -n "${NOTARY_PROFILE:-}" ]; then
  set -- --keychain-profile "$NOTARY_PROFILE"
elif [ -n "${NOTARY_KEY:-}" ]; then
  set -- --key "$NOTARY_KEY" --key-id "${NOTARY_KEY_ID:?NOTARY_KEY_ID is not set}" \
    --issuer "${NOTARY_ISSUER:?NOTARY_ISSUER is not set}"
else
  echo "no notary credentials: set NOTARY_PROFILE, or NOTARY_KEY, NOTARY_KEY_ID and NOTARY_ISSUER" >&2
  exit 1
fi
[ "$ZIP" = --check ] && exit 0

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
echo "notarizing $(basename "$ZIP")..." >&2
xcrun notarytool submit "$ZIP" "$@" --wait --timeout 2h --output-format json > "$WORK/notary.json" || true
STATUS="$(plutil -extract status raw -o - "$WORK/notary.json" 2>/dev/null || echo unknown)"
if [ "$STATUS" != Accepted ]; then
  echo "notarization ended $STATUS:" >&2
  cat "$WORK/notary.json" >&2
  ID="$(plutil -extract id raw -o - "$WORK/notary.json" 2>/dev/null || true)"
  [ -z "$ID" ] || xcrun notarytool log "$ID" "$@" >&2 || true
  exit 1
fi
echo "notarized: $(plutil -extract id raw -o - "$WORK/notary.json")" >&2
