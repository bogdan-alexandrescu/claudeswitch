#!/bin/sh
# After a Claude in Chrome tool call fails (PostToolUseFailure): when the
# extension is on another claude.ai account, or no browser answers,
# `claudeswitch chrome hint` tells Claude which account Claude Code is on and
# the command that opens that account's Chrome profile, once per rotation. It
# reads state.json and the config only: no keychain, no API, no browser.
# Successful calls are not hooked: their output is page content.
#
# Only a line of ours ever reaches Claude. A binary older than this plugin has
# no `chrome` command and exits 2 with its usage text, which must not be shown
# as a browser tool's error, so the binary's stderr is captured and forwarded
# only when it starts with "[claudeswitch]". Every other case exits 0.

bin=$(command -v claudeswitch 2>/dev/null || true)
if [ -z "$bin" ] && [ -x "$HOME/.local/bin/claudeswitch" ]; then
  bin="$HOME/.local/bin/claudeswitch"
fi
[ -n "$bin" ] || exit 0

# stdin (the hook's JSON) goes to the binary; its stderr comes back here and
# its stdout is discarded.
msg=$("$bin" chrome hint 2>&1 >/dev/null)
code=$?
case "$msg" in
  "[claudeswitch]"*)
    if [ "$code" -eq 2 ]; then
      printf '%s\n' "$msg" >&2
      exit 2
    fi
    ;;
esac
exit 0
