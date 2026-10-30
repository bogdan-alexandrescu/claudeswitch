#!/bin/sh
# For the release workflow: put the Developer ID certificate in a keychain of
# its own, unlocked for codesign only, and the notary API key beside it, then
# hand their names to the job's later steps.
#
#   CERT_P12=<base64 .p12> CERT_PASSWORD=<its password> NOTARY_KEY_P8=<.p8 text> \
#     macos/scripts/ci-keychain.sh            # sets up; writes $GITHUB_ENV
#   macos/scripts/ci-keychain.sh --remove     # removes both
#
# The later steps get SIGN_IDENTITY, SIGN_KEYCHAIN and NOTARY_KEY, which
# build-app.sh, app-zip.sh, sign-cli.sh and notarize.sh read.
set -eu

KC="${RUNNER_TEMP:?not in a GitHub Actions job}/signing.keychain-db"
P8="$RUNNER_TEMP/notary.p8"
if [ "${1:-}" = --remove ]; then
  security delete-keychain "$KC" 2>/dev/null || true
  rm -f "$P8"
  exit 0
fi

PW="$(openssl rand -hex 24)"
security create-keychain -p "$PW" "$KC"
security set-keychain-settings -lut 3600 "$KC"
security unlock-keychain -p "$PW" "$KC"
printf '%s' "${CERT_P12:?CERT_P12 is not set}" | base64 --decode > "$RUNNER_TEMP/cert.p12"
security import "$RUNNER_TEMP/cert.p12" -k "$KC" -P "${CERT_PASSWORD:?CERT_PASSWORD is not set}" \
  -T /usr/bin/codesign >/dev/null
rm -f "$RUNNER_TEMP/cert.p12"
security set-key-partition-list -S apple-tool:,apple: -s -k "$PW" "$KC" >/dev/null
# codesign finds an identity only in a keychain on the search list.
# shellcheck disable=SC2046 # one word per keychain path
security list-keychains -d user -s "$KC" $(security list-keychains -d user | tr -d '"')

ID="$(security find-identity -v -p codesigning "$KC" | sed -n 's/.*"\(Developer ID Application: .*\)"$/\1/p' | head -1)"
[ -n "$ID" ] || { echo "::error::CERT_P12 holds no Developer ID Application identity"; exit 1; }
echo "signing as $ID"
(umask 077; printf '%s\n' "${NOTARY_KEY_P8:?NOTARY_KEY_P8 is not set}" > "$P8")
{
  echo "SIGN_IDENTITY=$ID"
  echo "SIGN_KEYCHAIN=$KC"
  echo "NOTARY_KEY=$P8"
} >> "${GITHUB_ENV:?not in a GitHub Actions job}"
