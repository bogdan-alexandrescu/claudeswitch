#!/bin/sh
# Regenerate the Swift test fixtures from real Go output.
#
# It builds claudeswitch from this checkout and runs it against a throwaway
# HOME holding a placeholder config and state, so the fixtures are exactly what
# the binary writes and prints — never a copy of anyone's live state.json, which
# carries real account and organization ids.
#
# Only keychain-free commands run here (`why --json`, `config --json`,
# `version`). state.json is the file the binary itself saved: the seed holds one
# record the config does not mention, `load` discards it, and the save that
# follows rewrites the whole file in Go's own encoding.
#
#   macos/scripts/gen-fixtures.sh
#
#   SUFFIX=-profiles macos/scripts/gen-fixtures.sh
#
# The first writes the single-profile shapes (no [[profile]] blocks; the seed
# state still carries a second profile, which the binary keeps). SUFFIX adds
# two [[profile]] blocks and writes only why$SUFFIX.json, the per-profile
# shape. SRC=<other checkout> builds another tree instead of this one.
set -eu

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/macos/Tests/ClaudeSwitchCoreTests/Fixtures"
SRC="${SRC:-$ROOT}"
SUFFIX="${SUFFIX:-}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "building claudeswitch from $SRC"
(cd "$SRC" && go build -o "$WORK/claudeswitch" ./cmd/claudeswitch)

H="$WORK/home"
mkdir -p "$H/.config/claudeswitch" "$H/.local/state/claudeswitch"

# Times relative to now, so the binary reasons about a live-looking picture.
iso() { date -u -v"$1" +%Y-%m-%dT%H:%M:%SZ; }
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
AGO1=$(iso -1M)
AGO3=$(iso -3M)
IN2H=$(iso +2H)
IN3D=$(iso +3d)
IN40M=$(iso +40M)
IN5D=$(iso +5d)

cat > "$H/.config/claudeswitch/config.toml" <<EOF
switch_at = 90
switch_at_weekly = 95
hard_floor = 98

[[account]]
id = "work-1"
account_uuid = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
org_id = "11111111-1111-1111-1111-111111111111"

[[account]]
id = "work-2"
account_uuid = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
org_id = "22222222-2222-2222-2222-222222222222"

[[account]]
id = "personal"
account_uuid = "cccccccc-cccc-cccc-cccc-cccccccccccc"
org_id = "33333333-3333-3333-3333-333333333333"
EOF

cat > "$H/.local/state/claudeswitch/state.json" <<EOF
{
  "version": 1,
  "daemon_live": false,
  "daemon_since": "$AGO3",
  "saved_at": "$AGO1",
  "profiles": {
    "default": {"active_account": "work-1", "last_switch": "$AGO3", "active_at": "$AGO3"},
    "review": {"active_account": "personal", "pinned": "personal"}
  },
  "accounts": {
    "work-1": {
      "id": "work-1",
      "org_id": "11111111-1111-1111-1111-111111111111",
      "seat": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@11111111-1111-1111-1111-111111111111",
      "last_at": "$AGO1",
      "prev_worst": 40.5,
      "prev_at": "$AGO3",
      "last_usage": {
        "five_hour": {"utilization": 3.0, "resets_at": "$IN2H"},
        "seven_day": {"utilization": 41.0, "resets_at": "$IN3D"},
        "limits": [
          {"kind": "session", "group": "session", "percent": 3, "severity": "normal", "is_active": false, "resets_at": "$IN2H"},
          {"kind": "weekly_all", "group": "weekly", "percent": 41, "severity": "normal", "is_active": true, "resets_at": "$IN3D"},
          {"kind": "weekly_scoped", "group": "weekly", "percent": 12, "severity": "normal", "is_active": false, "resets_at": "$IN3D",
           "scope": {"model": {"id": null, "display_name": "Modelname"}, "surface": null}}
        ],
        "spend": {"used": {"amount_minor": 0, "currency": "USD", "exponent": 2}, "limit": null, "percent": 0, "severity": "normal", "enabled": false},
        "extra_usage": {"is_enabled": false}
      }
    },
    "work-2": {
      "id": "work-2",
      "org_id": "22222222-2222-2222-2222-222222222222",
      "seat": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb@22222222-2222-2222-2222-222222222222",
      "last_at": "$AGO1",
      "burnt_until": "$IN40M",
      "burnt_window": "five_hour",
      "last_usage": {
        "five_hour": {"utilization": 100.0, "resets_at": "$IN40M"},
        "seven_day": {"utilization": 77.0, "resets_at": "$IN5D"},
        "limits": [
          {"kind": "session", "group": "session", "percent": 100, "severity": "exceeded", "is_active": true, "resets_at": "$IN40M"}
        ],
        "spend": {"used": {"amount_minor": 0, "currency": "USD", "exponent": 2}, "limit": null, "percent": 0, "severity": "normal", "enabled": false},
        "extra_usage": {"is_enabled": false}
      }
    },
    "personal": {
      "id": "personal",
      "org_id": "33333333-3333-3333-3333-333333333333",
      "seat": "cccccccc-cccc-cccc-cccc-cccccccccccc@33333333-3333-3333-3333-333333333333",
      "last_at": "$AGO3",
      "last_error": "usage API: 401 Unauthorized",
      "last_usage": {
        "five_hour": {"utilization": 12.0, "resets_at": "$IN2H"},
        "seven_day": {"utilization": 8.0, "resets_at": "$IN5D"}
      }
    },
    "stale-old": {"id": "stale-old", "last_at": "$AGO3"}
  }
}
EOF

# For the per-profile shape, a config naming a second profile.
if [ -n "$SUFFIX" ]; then
cat >> "$H/.config/claudeswitch/config.toml" <<EOF

[[profile]]
name = "default"

[[profile]]
name = "review"
dir = "$H/.claude-review"
EOF
mkdir -p "$H/.claude-review"
fi

# From inside the throwaway HOME, so `why` reports a placeholder directory.
HP="$(cd "$H" && pwd -P)"
run() { (cd "$H" && HOME="$H" CLAUDE_CONFIG_DIR= "$WORK/claudeswitch" "$@"); }

if [ -n "$SUFFIX" ]; then
  run why --json | sed "s#$HP#/Users/example#g; s#$H#/Users/example#g" > "$OUT/why$SUFFIX.json"
  echo "wrote $OUT/why$SUFFIX.json"
  exit 0
fi
run why --json > "$OUT/why.json"
run config --json > "$OUT/config.json"
run version > "$OUT/version.txt"
cp "$H/.local/state/claudeswitch/state.json" "$OUT/state.json"

# The app's JSON CLI (docs/APP_CLI.md): the commands that read and edit
# config.toml alone, run after the reads above because they change it. A
# refusal exits 1 with the error object on stdout, which is the fixture.
# Commands that reach the keychain, launchd or Chrome (profile list, login,
# add, account delete, recovery, daemon, chrome add and open) are never run
# here: their fixtures are written by hand from APP_CLI.md and named
# cli-*.json as well.
gen() { # gen <fixture> <args...>
  f="$1"; shift
  run "$@" > "$OUT/$f" || true
}
gen cli-config-schema.json config schema --json
gen cli-config-get.json config get switch_at --json
gen cli-config-set.json config set switch_at 85 --json
gen cli-error-invalid-value.json config set switch_at 200 --json
gen cli-error-not-found.json config set no_such_setting 1 --json
gen cli-priority.json priority personal work-1 --json
gen cli-error-usage.json priority --json
gen cli-chrome-list.json chrome list --json

# Lane 15, both keychain-free: `account list` with an email and a plan
# recorded in state (added here, after state.json was copied, so that
# fixture keeps none), and `profile remove` on a config with two profiles.
STATE="$H/.local/state/claudeswitch/state.json"
sed -i '' '1a\
  "emails": {"work-1": "person1@example.com"}, "plans": {"work-1": "Max 20x"},
' "$STATE"
cat >> "$H/.config/claudeswitch/config.toml" <<EOF

[[profile]]
name = "default"
pool = ["work-1", "work-2"]

[[profile]]
name = "review"
dir = "$H/.claude-review"
pool = ["personal"]
EOF
mkdir -p "$H/.claude-review"
gen cli-account-list.json account list --json
gen cli-error-profile-remove-confirm.json profile remove review --json
gen cli-profile-remove.json profile remove review --yes --json
gen cli-error-last-profile.json profile remove default --yes --json

# The generated files must hold nothing but placeholders.
if grep -l "stale-old" "$OUT/state.json" >/dev/null; then
  echo "state.json was not re-saved by the binary" >&2; exit 1
fi
sed -i '' "s#$HP#/Users/example#g; s#$H#/Users/example#g" "$OUT"/*.json
echo "wrote fixtures to $OUT (generated at $NOW)"
