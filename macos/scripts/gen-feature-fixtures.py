#!/usr/bin/env python3
"""Write the test fixtures for F1-F5, F7 and F12 (docs/IMPROVEMENTS.md,
"Next features"), built against the spec's JSON before the CLI prints it.

Each is the current fixture (gen-fixtures.sh, real Go output) with the
spec's new keys added, or, for the new commands, the spec's shape filled
with placeholder names. Regenerate the base fixtures from the real binary
once the Go side lands, and these from them.

    python3 macos/scripts/gen-feature-fixtures.py
"""
import json
import math
import os
from datetime import datetime, timedelta, timezone

HERE = os.path.dirname(os.path.abspath(__file__))
FX = os.path.join(HERE, "..", "Tests", "ClaudeSwitchCoreTests", "Fixtures")
NOW = datetime(2026, 10, 8, 2, 46, 41, tzinfo=timezone.utc)  # render.sh's FIXED_NOW


def load(name):
    with open(os.path.join(FX, name)) as f:
        return json.load(f)


def save(name, obj):
    with open(os.path.join(FX, name), "w") as f:
        json.dump(obj, f, indent=2, sort_keys=True)
        f.write("\n")


def ts(d):
    return d.strftime("%Y-%m-%dT%H:%M:%SZ")


# F2: why --json with pool_dry_at per profile and trigger_at per account.
w = load("why-profiles.json")
w["profiles"][0]["pool_dry_at"] = "2026-10-08T14:00:00Z"
w["profiles"][1]["pool_dry_at"] = None
eta = {"work-1": "2026-10-08T09:30:00Z", "personal": None, "work-2": "2026-10-10T18:15:00Z"}
for a in w["profiles"][0]["accounts"]:
    a["trigger_at"] = eta[a["id"]]
save("why-runway.json", w)

# F3 and F5: account list --json with pin_hard and refresh_expires_at.
l = load("cli-account-list.json")
exp = {"personal": "2026-10-10T02:00:00Z", "work-1": "2026-11-20T00:00:00Z", "work-2": None}
for a in l["accounts"]:
    a["pin_hard"] = a["id"] == "personal"
    a["refresh_expires_at"] = exp.get(a["id"])
save("cli-account-list-next.json", l)

# F4: profile list --json with paths.
p = load("cli-profile-list.json")
p["profiles"][0]["paths"] = []
p["profiles"][1]["paths"] = ["~/review/**", "~/clients/acme/**"]
save("cli-profile-list-paths.json", p)

# F1: config schema --json with prefer.
s = load("cli-config-schema.json")
s["settings"].insert(5, {
    "default": "room", "enum": ["room", "expiring"], "key": "prefer", "max": None, "min": None,
    "min_exclusive": False, "scopes": ["global", "profile"], "type": "enum",
    "description": "which eligible account rotation spends first: \"room\" (the most room) or "
                   "\"expiring\" (the weekly quota that resets soonest while it is unused)",
})
save("cli-config-schema-prefer.json", s)

# F12: doctor --json.
save("cli-doctor.json", {"checks": [
    {"name": "config", "status": "ok", "message": "~/.config/claudeswitch/config.toml: 3 accounts, 2 profiles", "fix": None},
    {"name": "vault", "status": "ok", "message": "3 accounts in the vault", "fix": None},
    {"name": "keychain", "status": "fail", "message": "the daemon cannot read the vault without a prompt", "fix": "keychain allow"},
    {"name": "login personal", "status": "fail", "message": "personal was refused (401): it needs signing in again", "fix": "signin personal"},
    {"name": "refresh work-2", "status": "warn", "message": "work-2's login expires in 3 days", "fix": "signin work-2"},
    {"name": "usage API", "status": "ok", "message": "reachable; last read 1m ago", "fix": None},
    {"name": "daemon", "status": "fail", "message": "installed, but it has not polled for 14m", "fix": "daemon restart"},
    {"name": "status line", "status": "fail", "message": "Claude Code's settings.json has no status line", "fix": "statusline install"},
    {"name": "plugin", "status": "warn", "message": "the cs plugin is one release behind", "fix": "plugin update"},
]})

# F3: the audit log's unpin events, among others (JSONL; one bad line).
events = [
    {"at": "2026-10-07T20:00:00Z", "kind": "switch", "profile": "default", "from": "work-2", "to": "work-1", "reason": "work-2 at 91%"},
    {"at": "2026-10-07T23:10:00Z", "kind": "unpin", "profile": "review", "account": "personal", "reason": "it needs signing in"},
    {"at": "2026-10-08T01:30:00Z", "kind": "decision", "profile": "default", "decision": "stay"},
    {"at": "2026-10-08T02:15:00Z", "kind": "unpin", "profile": "default", "account": "work-2", "reason": "it was refused"},
]
with open(os.path.join(FX, "audit-unpin.jsonl"), "w") as f:
    for e in events:
        f.write(json.dumps(e, sort_keys=True) + "\n")
    f.write("not json\n")

# F7: history --usage --days 30 --json. A reading every 2 hours per account:
# the session climbs and resets every 5 hours while the account is used, the
# week climbs over 7 days and resets. Rotation moves between them.
start = NOW - timedelta(days=30)
accounts = {"work-1": 0.0, "work-2": 2.5, "personal": 5.0}  # weekly reset offset, days
pace = {"work-1": 26.0, "work-2": 22.0, "personal": 14.0}     # weekly points a day
series = {a: [] for a in accounts}
switches = []
live = "work-1"
order = ["work-1", "work-2", "personal"]
t = start
step = timedelta(hours=2)
week = {a: 0.0 for a in accounts}
while t <= NOW:
    h = (t - start).total_seconds() / 3600
    for a, off in accounts.items():
        days = (t - start).total_seconds() / 86400 + off
        if int(days / 7) != int((days - 2 / 24) / 7):
            week[a] = 0.0
        busy = a == live
        day_hour = (t.hour + 2) % 24
        working = 8 <= day_hour <= 22
        if busy and working:
            week[a] = min(100.0, week[a] + pace[a] / 7)
        sess = 0.0
        if busy and working:
            phase = (h % 5) / 5
            sess = round(min(100.0, 18 + 70 * phase + 6 * math.sin(h)), 1)
        series[a].append({"at": ts(t), "five_hour": sess, "seven_day": round(week[a], 1)})
    # Rotate away from an account near its weekly trigger, or every 4 days.
    if week[live] >= 92 or (t - start).total_seconds() % (4 * 86400) == 0 and t != start:
        nxt = min((x for x in order if x != live), key=lambda x: week[x])
        switches.append({"at": ts(t), "profile": "default", "from": live, "to": nxt,
                         "reason": f"{live} at {week[live]:.0f}% of the week"})
        live = nxt
    t += step
# One gap: the daemon was off for half a day ten days ago (no readings).
gap_from, gap_to = NOW - timedelta(days=10, hours=12), NOW - timedelta(days=10)
for a in series:
    series[a] = [r for r in series[a] if not (ts(gap_from) <= r["at"] < ts(gap_to))]
# An unknown reading (an error poll): null, never 0.
series["personal"][-3]["five_hour"] = None
save("cli-history-usage.json", {
    "days": 30, "from": ts(start), "to": ts(NOW),
    "accounts": [{"id": a, "series": series[a]} for a in order],
    "switches": switches,
})
