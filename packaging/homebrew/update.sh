#!/bin/sh
# Fill in the Homebrew formula and cask for a release (IMPROVEMENTS F8).
#
#   packaging/homebrew/update.sh <version> <checksums.txt> [<formula.rb> [<cask.rb>]]
#
# <version> is the tag, with or without its v (v0.5.6 or 0.5.6).
# <checksums.txt> is the release's checksums.txt ("<sha256>  <file>" lines,
# as shasum -a 256 writes them). It must name all four archives
# (claudeswitch_v<version>_{darwin,linux}_{arm64,amd64}.tar.gz). The app
# zip's line (ClaudeSwitch-<version>-macos.zip, from its .sha256 file) may be
# appended to it; without it the cask is left as it is.
#
# The files default to the templates beside this script and are edited in
# place, so the same command updates a tap's already-filled copies. Every
# value is checked before anything is written; a refusal changes nothing.
set -eu

die() { echo "update.sh: $*" >&2; exit 1; }

[ $# -ge 2 ] && [ $# -le 4 ] || die "usage: update.sh <version> <checksums.txt> [<formula.rb> [<cask.rb>]]"
here="$(cd "$(dirname "$0")" && pwd)"
version="${1#v}"
sums="$2"
formula="${3:-$here/claudeswitch.rb}"
cask="${4:-$here/claudeswitch-app.rb}"

printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$' ||
  die "version \"$1\" is not like 0.5.6 or v0.5.6"
[ -r "$sums" ] || die "cannot read $sums"
[ -w "$formula" ] || die "cannot write $formula"

# sum_of <file name>: its sha256 from the checksums file, "" when absent.
sum_of() {
  awk -v f="$1" '{ n = $2; sub(/^\*/, "", n) } n == f { print $1; exit }' "$sums"
}

check_sum() {
  printf '%s\n' "$1" | grep -Eq '^[0-9a-f]{64}$' || die "bad or missing sha256 for $2: \"$1\""
}

targets="darwin_arm64 darwin_amd64 linux_arm64 linux_amd64"
pairs=""
for target in $targets; do
  file="claudeswitch_v${version}_${target}.tar.gz"
  s="$(sum_of "$file")"
  check_sum "$s" "$file"
  pairs="$pairs $target=$s"
done

zip="ClaudeSwitch-${version}-macos.zip"
zipsum="$(sum_of "$zip")"
if [ -n "$zipsum" ]; then
  check_sum "$zipsum" "$zip"
  [ -w "$cask" ] || die "cannot write $cask"
fi

# rewrite <file> <awk program> [awk -v assignments...]: through a temporary
# file beside it, then moved over it, keeping its mode.
rewrite() {
  file="$1"; prog="$2"; shift 2
  tmp="$file.update.$$"
  awk "$@" "$prog" "$file" > "$tmp" || { rm -f "$tmp"; die "could not rewrite $file"; }
  cat "$tmp" > "$file"
  rm -f "$tmp"
}

# The formula: its version line, and each sha256 line by its target comment.
rewrite "$formula" '
  BEGIN { n = split(pairs, kv, " "); for (i = 1; i <= n; i++) { split(kv[i], p, "="); sum[p[1]] = p[2] } }
  /^  version "[^"]*"$/ { sub(/"[^"]*"/, "\"" version "\"") }
  /sha256 "[0-9a-f]*" # [a-z]+_[a-z0-9]+$/ {
    t = $NF
    if (t in sum) { sub(/sha256 "[0-9a-f]*"/, "sha256 \"" sum[t] "\""); done[t] = 1 }
  }
  { print }
  END { for (t in sum) if (!(t in done)) { print "no sha256 line for " t > "/dev/stderr"; exit 1 } }
' -v version="$version" -v pairs="$pairs"
echo "formula: $formula is claudeswitch $version"

if [ -n "$zipsum" ]; then
  rewrite "$cask" '
    /^  version "[^"]*"$/ { sub(/"[^"]*"/, "\"" version "\"") }
    /^  sha256 "[0-9a-f]*"$/ { sub(/"[0-9a-f]*"/, "\"" sum "\"") }
    { print }
  ' -v version="$version" -v sum="$zipsum"
  echo "cask: $cask is ClaudeSwitch $version"
else
  echo "cask: no $zip line in $sums; $cask left as it was" >&2
fi
