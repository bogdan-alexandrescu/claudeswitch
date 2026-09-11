#!/bin/sh
# Install claudeswitch and register it with launchd.
#
# Dry-run by default: the daemon reports the swaps it would make and changes
# nothing until you edit the plist and add --live.
set -eu

# --live installs the daemon in acting mode. Default is dry-run: it reports the
# swaps it would make and changes nothing.
LIVE_ARG=""
MODE="dry-run: it will report, not act"
if [ "${1:-}" = "--live" ]; then
  LIVE_ARG="    <string>--live</string>"
  MODE="LIVE: it will perform swaps"
fi

BIN_DIR="${HOME}/.local/bin"
STATE_DIR="${HOME}/.local/state/claudeswitch"
LABEL="xyz.claudeswitch.daemon"
PLIST="${HOME}/Library/LaunchAgents/${LABEL}.plist"
UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
UNIT="${UNIT_DIR}/claudeswitch.service"
OS="$(uname -s)"

echo "building..."
go build -o "bin/claudeswitch" ./cmd/claudeswitch

mkdir -p "$BIN_DIR" "$STATE_DIR"
chmod 700 "$STATE_DIR"
install -m 0755 bin/claudeswitch "$BIN_DIR/claudeswitch"
echo "installed $BIN_DIR/claudeswitch"

# A symlink rather than a shell alias: it works in non-interactive shells,
# in scripts, and from Claude Code's `!` prefix, which an alias does not.
if [ -e "$BIN_DIR/cs" ] && [ ! -L "$BIN_DIR/cs" ]; then
  echo "note: $BIN_DIR/cs exists and is not our symlink; leaving it alone"
elif [ "$(readlink "$BIN_DIR/cs" 2>/dev/null)" = "claudeswitch" ]; then
  : # already ours
else
  ln -sf claudeswitch "$BIN_DIR/cs"
  echo "installed $BIN_DIR/cs -> claudeswitch"
fi


if [ "$OS" = "Darwin" ]; then
  # Force the Keychain prompt NOW, while someone is at the keyboard.
  #
  # macOS ties an item's access approval to the exact binary that was approved,
  # so a rebuilt binary is a stranger and prompts again. A daemon under launchd
  # has no GUI session to answer that prompt, and security(1) waits for an answer
  # forever — leaving the daemon alive, silent, polling nothing. Asking here
  # turns an invisible hang into one click.
  echo
  echo "checking Keychain access (macOS may ask — click Always Allow)…"
  if "$BIN_DIR/claudeswitch" whoami >/dev/null 2>&1; then
    echo "  ok"
  else
    echo "  could not read the credential. Run this once and approve the prompt:"
    echo "      claudeswitch whoami"
    echo "  then re-run ./install.sh"
    exit 1
  fi

  mkdir -p "$(dirname "$PLIST")"
  cat > "$PLIST" <<PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>${LABEL}</string>
  <key>ProgramArguments</key>
  <array>
    <string>${BIN_DIR}/claudeswitch</string>
    <string>watch</string>
${LIVE_ARG}
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key>
  <dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>30</integer>
  <key>StandardOutPath</key><string>${STATE_DIR}/daemon.log</string>
  <key>StandardErrorPath</key><string>${STATE_DIR}/daemon.log</string>
  <!-- No ProcessType. "Background" looks right for a poller and is a trap: it
       puts the job in launchd's background QoS band, where a `security`
       child never gets far enough to read the keychain. Measured on macOS
       15: a read that takes 0.1s from a shell and 2.1s from a plain agent
       never completes at all under Background. -->
</dict>
</plist>
PLIST_EOF
  echo "wrote $PLIST"

  launchctl unload "$PLIST" 2>/dev/null || true
  launchctl load "$PLIST"
  echo "loaded $LABEL ($MODE)"
  echo
  echo "to uninstall:  launchctl unload $PLIST && rm $PLIST"

elif [ "$OS" = "Linux" ]; then
  # systemd --user, so the daemon runs as you and can reach your credentials.
  mkdir -p "$UNIT_DIR"
  LIVE_FLAG=""
  [ -n "$LIVE_ARG" ] && LIVE_FLAG=" --live"
  cat > "$UNIT" <<UNIT_EOF
[Unit]
Description=claudeswitch — quota-aware Claude account rotation
After=network-online.target

[Service]
Type=simple
ExecStart=${BIN_DIR}/claudeswitch watch${LIVE_FLAG}
Restart=always
RestartSec=30
# The daemon holds credentials; keep its files to itself.
UMask=0077

[Install]
WantedBy=default.target
UNIT_EOF
  echo "wrote $UNIT"

  systemctl --user daemon-reload
  systemctl --user enable --now claudeswitch.service
  echo "started claudeswitch.service ($MODE)"
  echo
  echo "  logs:       journalctl --user -u claudeswitch -f"
  echo "  uninstall:  systemctl --user disable --now claudeswitch && rm $UNIT"

else
  echo "unsupported platform: $OS"
  echo "claudeswitch itself works; you will need to run \`claudeswitch watch\` yourself."
  exit 1
fi

echo
echo "next:"
echo "  cs doctor      # check everything"
echo "  cs status      # what every account has left"
echo "  cs audit       # what it has decided"
