#!/bin/sh
# SessionStart: hand Claude a short reading of quota. Whatever happens, this
# exits 0 — a plugin must never be the reason a session fails to start.
#
# All the logic lives in `claudeswitch context`, which reads state.json only:
# no keychain, no API. This script only has to find the binary.

bin=$(command -v claudeswitch 2>/dev/null || true)
# Claude Code started from a GUI does not inherit a login shell's PATH, so
# look where install.sh puts it before concluding it is missing.
if [ -z "$bin" ] && [ -x "$HOME/.local/bin/claudeswitch" ]; then
  bin="$HOME/.local/bin/claudeswitch"
fi
if [ -z "$bin" ]; then
  echo "[claudeswitch] plugin installed but \`claudeswitch\` is not on PATH; see https://github.com/bogdan-alexandrescu/claudeswitch#install"
  exit 0
fi

# A binary older than the plugin has no `context` command and exits non-zero.
if ! "$bin" context 2>/dev/null; then
  echo "[claudeswitch] the installed binary is older than this plugin; update claudeswitch"
fi
exit 0
