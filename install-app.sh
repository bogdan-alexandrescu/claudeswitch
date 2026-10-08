#!/bin/sh
# Build the claudeswitch menu-bar app on this Mac and install it.
#
#   ./install-app.sh              build, install, offer to open it
#   ./install-app.sh --open       ... and open it without asking
#   ./install-app.sh --no-open    ... and do not
#   ./install-app.sh --uninstall  quit it and remove it
#
# Needs only the Command Line Tools (xcode-select --install), not Xcode. A copy
# built here was never downloaded, so it carries no quarantine flag and opens
# without a Gatekeeper warning; no Apple Developer account is involved.
#
# The app only reads: it runs `claudeswitch why --json` / `config --json`,
# reads ~/.local/state/claudeswitch/state.json, and switches by running
# `claudeswitch use`. Install the binary first (./install.sh).
set -eu

cd "$(dirname "$0")"
NAME="ClaudeSwitch.app"
OPEN=ask
case "${1:-}" in
  --open) OPEN=yes ;;
  --no-open) OPEN=no ;;
  --uninstall) OPEN=uninstall ;;
  "") ;;
  *) sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac

[ "$(uname -s)" = Darwin ] || { echo "the menu-bar app is macOS only" >&2; exit 1; }

quit_running() {
  if pgrep -xq ClaudeSwitch; then
    osascript -e 'tell application id "xyz.claudeswitch.menubar" to quit' >/dev/null 2>&1 || true
    sleep 1
    pkill -x ClaudeSwitch 2>/dev/null || true
  fi
}

if [ "$OPEN" = uninstall ]; then
  quit_running
  removed=
  for d in /Applications "$HOME/Applications"; do
    if [ -d "$d/$NAME" ]; then rm -rf "${d:?}/$NAME" && echo "removed $d/$NAME"; removed=1; fi
  done
  [ -n "$removed" ] || echo "ClaudeSwitch.app is not installed"
  exit 0
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
APP="$(macos/scripts/build-app.sh "$WORK")"

# /Applications when this user can write there (an admin account usually can),
# otherwise ~/Applications, which Launchpad and Spotlight index too.
DEST=/Applications
if [ ! -w "$DEST" ]; then
  DEST="$HOME/Applications"
  mkdir -p "$DEST"
fi

quit_running
rm -rf "${DEST:?}/$NAME"
ditto "$APP" "$DEST/$NAME"
echo "installed $DEST/$NAME"

if ! [ -x "$HOME/.local/bin/claudeswitch" ] && ! command -v claudeswitch >/dev/null 2>&1; then
  echo "note: the claudeswitch binary is not installed yet; the app will say so until it is."
  echo "      install it with ./install.sh (or a release binary in ~/.local/bin)."
fi

if [ "$OPEN" = ask ]; then
  if [ -t 0 ]; then
    printf "open it now? [Y/n] "
    read -r ans || ans=n
    case "$ans" in [nN]*) OPEN=no ;; *) OPEN=yes ;; esac
  else
    OPEN=no
  fi
fi
if [ "$OPEN" = yes ]; then
  open "$DEST/$NAME"
  echo "it is in the menu bar. Launch at login is a checkbox in its menu."
else
  echo "open it with: open \"$DEST/$NAME\""
fi
