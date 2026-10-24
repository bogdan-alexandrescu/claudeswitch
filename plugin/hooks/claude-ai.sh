#!/bin/sh
# Around Claude in Chrome, Artifact and Claude Docs calls (IMPROVEMENTS
# phase 1 S and G): `claudeswitch hook pre` before one waits until
# settle_after_swap after the profile's last switch, so the call acts as the
# account now live, and refuses a call on an artifact another account
# published, saying whose it is, and on a session's first Claude in Chrome
# call after a switch tells Claude its earlier tabs stayed in the previous
# account's Chrome profile; `claudeswitch hook post` after a publish records
# its owner. They read the config, state.json and the plugin's artifact and
# session books only: no keychain, no API, no browser.
#
# usage: claude-ai.sh pre|post   (the hook's JSON on stdin)
#
# Only an answer of ours ever reaches Claude Code: the binary's stdout is
# forwarded when it is hook JSON, and nothing else. A binary older than this
# plugin has no `hook` command; its usage text is dropped, and the call goes
# on. Every case exits 0, so the plugin is never why a tool call fails.

bin=$(command -v claudeswitch 2>/dev/null || true)
if [ -z "$bin" ] && [ -x "$HOME/.local/bin/claudeswitch" ]; then
  bin="$HOME/.local/bin/claudeswitch"
fi
[ -n "$bin" ] || exit 0

out=$("$bin" hook "$1" 2>/dev/null)
case "$out" in
  '{"hookSpecificOutput"'*) printf '%s\n' "$out" ;;
esac
exit 0
