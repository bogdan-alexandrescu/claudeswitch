#!/usr/bin/env python3
"""Write the --render fixture overlays in macos/RenderFixtures.

Each set is a directory of the JSON files that differ from the test
fixtures (macos/Tests/ClaudeSwitchCoreTests/Fixtures); scripts/render.sh
lays it over a copy of those. Placeholder names only: nothing here comes
from anyone's state.

  many         five profiles, a dozen accounts, long names and paths,
               needs-login, refused and errored accounts, an account in
               default by D6 alone, an empty profile, two ghosts
  one-profile  no [[profile]] blocks: the implicit default profile
  hero         two healthy profiles, no warnings: the README's hero image
  tutorial     the CLI screenshots' story, after work rotated to work-team,
               with the Chrome sign-in notice (README app section, TUTORIAL)
  pools        shared pools: two [[pool]] blocks both profiles list (team
               second in each), one pool each lists first, an empty pool
               no profile lists, an account in no pool, and team-2 live in
               work, so in use for default
  older-cli    a binary from before shared pools: no pool keys, and
               `pool list` answers `usage`
  states       the states no other set reaches, as one story: a daemon not
               polling for 21m; default's live account refused with nothing
               else usable (Next: wait); work's pin lifted when its account
               was refused, its pool running dry in 40m with a trigger time
               in its picker; lab pinned hard to a refused account, its best
               short of the landing margin; client pinned softly, already
               on its best, a read that timed out and a window reset since

The sets before pools are configs with no [[pool]] blocks: write() gives
them what the CLI reports for one (each profile its own pool, named after
it, and the accounts no pool lists), unless a set brings its own.

    python3 macos/scripts/gen-render-fixtures.py
"""
import datetime
import json
import os
import re
import shutil

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "RenderFixtures")
NOW = "2026-10-08T02:45:41Z"
# The clock render.sh draws the overlay sets at (its FIXED_NOW).
RENDER_NOW = "2026-10-08T02:46:41Z"
Z = "0001-01-01T00:00:00Z"
IN2H = "2026-10-08T04:46:41Z"
IN3D = "2026-10-11T02:46:41Z"
SEAT = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@11111111-1111-1111-1111-111111111111"


def rec(id, fh, sd, err=None, burnt=None, models=(), last=NOW, fh_reset=IN2H, refresh="2026-11-08T02:45:41Z"):
    limits = [
        {"kind": "session", "group": "session", "percent": fh, "severity": "normal",
         "is_active": False, "resets_at": fh_reset, "scope": None},
        {"kind": "weekly_all", "group": "weekly", "percent": sd, "severity": "normal",
         "is_active": True, "resets_at": IN3D, "scope": None},
    ]
    for m, p in models:
        limits.append({"kind": "weekly_scoped", "group": "weekly", "percent": p, "severity": "normal",
                       "is_active": False, "resets_at": IN3D,
                       "scope": {"model": {"id": None, "display_name": m}, "surface": None}})
    r = {"id": id, "org_id": "11111111-1111-1111-1111-111111111111", "seat": SEAT,
         "last_usage": {"five_hour": {"utilization": fh, "resets_at": fh_reset},
                        "seven_day": {"utilization": sd, "resets_at": IN3D},
                        "limits": limits},
         "last_at": last, "prev_at": Z, "burnt_until": burnt or Z,
         "refresh_expiry": refresh, "token_expiry": Z}
    if burnt:
        r["burnt_window"] = "five_hour"
    if err:
        r["last_error"] = err
    return r


def prof(name, pool, listed, dir=None, live=None, pinned=None, signed="yes", declared=True,
         from_env=False, sa=90, sw=95, ov=None):
    return {"name": name, "dir": dir, "from_env": from_env, "declared": declared, "pool": pool,
            "listed": listed, "live": live, "pinned": pinned, "signed_in": signed,
            "overrides": ov or {},
            "thresholds": {"switch_at": sa, "switch_at_weekly": sw, "hard_floor": 98, "landing_margin": 10}}


def why_prof(name, pool, active, decision, best=None, best_why=None, sa=90, sw=95):
    accts = [{"id": a, "active": a == active, "eligible": True, "utilization": 10 + i,
              "window": "five_hour", "why": "ready"} for i, a in enumerate(pool)]
    return {"profile": name, "pool": pool, "accounts": accts, "decision": decision,
            "current": name == "default", "best": best, "best_why": best_why,
            "thresholds": {"switch_at": sa, "switch_at_weekly": sw, "hard_floor": 98, "landing_margin": 10}}


def info(id, profile, active_in=None, plan="Max 20x", email=None, state="available"):
    return {"id": id, "email": email, "plan": plan, "seat": "aaaaaaaa@11111111", "enabled": True,
            "profile": profile, "active_in": active_in, "pinned": False, "state": state, "reading": None}


def own_pools(files):
    """What the CLI reports for a config with no [[pool]] blocks: each
    declared profile's own pool (its listed accounts; an empty one is no
    pool), the accounts no pool lists, and pool and profiles per account."""
    plist, alist = files["cli-profile-list.json"], files["cli-account-list.json"]
    pools, owner = [], {}
    for p in plist["profiles"]:
        p["pools"] = [p["name"]] if p.get("declared", True) else []
        if p.get("declared", True) and p["listed"]:
            pools.append({"name": p["name"], "accounts": p["listed"], "profiles": [p["name"]], "own": True})
            for a in p["listed"]:
                owner[a] = p["name"]
    plist["pools"] = pools
    has_default = any(p["name"] == "default" for p in plist["profiles"])
    unlisted = []
    for a in alist["accounts"]:
        a["pool"] = owner.get(a["id"])
        a["profiles"] = [owner[a["id"]]] if a["id"] in owner else (["default"] if has_default else [])
        if a["id"] not in owner:
            unlisted.append(a["id"])
    files["cli-pool-list.json"] = {"pools": pools, "unlisted": unlisted}


FIX = os.path.join(HERE, "..", "Tests", "ClaudeSwitchCoreTests", "Fixtures")


def base(n):
    with open(os.path.join(FIX, n)) as f:
        return json.load(f)


def when(s):
    return datetime.datetime.strptime(s, "%Y-%m-%dT%H:%M:%SZ")


def plural(n, word):
    return "%d %s%s" % (n, word, "" if n == 1 else "s")


# Go's state.NeedsLoginErr: errors only an interactive sign-in cures.
NEEDS_LOGIN = ("no stored credential", "not in the vault", "401", "re-login", "invalid_grant",
               "needs an interactive login")


def needs_signin(files):
    """The set's accounts that need sign-in, in its state's order."""
    st = files["state.json"]
    listed = {a["id"]: a.get("state") for a in files["cli-account-list.json"]["accounts"]}
    out = []
    for id, r in st["accounts"].items():
        e = r.get("last_error") or ""
        if any(k in e for k in NEEDS_LOGIN) or listed.get(id) == "needs_login":
            out.append((id, e))
    return out


def doctor(files, mixed):
    """`cs doctor --json` for the set's own story (QA 2026-10-28: each set's
    Health was a fixed healthy list, or the test fixture's, with accounts and
    a stale daemon the set does not have): its counts, a failing login check
    for each account that needs sign-in, and a failing daemon check when its
    daemon has stopped. mixed adds the warnings and failures the many set's
    Health shows, drawn from its own accounts. The test fixture's names and
    shape."""
    st = files["state.json"]
    n, m = len(st["accounts"]), len(files["cli-profile-list.json"]["profiles"])
    checks = [
        {"name": "config", "status": "ok", "fix": None,
         "message": "~/.config/claudeswitch/config.toml: %s, %s" % (plural(n, "account"), plural(m, "profile"))},
        {"name": "vault", "status": "ok", "fix": None, "message": "%s in the vault" % plural(n, "account")},
        {"name": "keychain", "status": "fail", "fix": "keychain allow",
         "message": "the daemon cannot read the vault without a prompt"} if mixed else
        {"name": "keychain", "status": "ok", "fix": None, "message": "the daemon can read the vault"}]
    for id, e in needs_signin(files):
        why = "was refused (401)" if "401" in e else "has no stored credential" if e else "needs a new login"
        checks.append({"name": "login " + id, "status": "fail", "fix": "signin " + id,
                       "message": "%s %s: it needs sign-in" % (id, why)})
    if mixed:
        soon = [id for id, r in st["accounts"].items()
                if r.get("refresh_expiry", Z) != Z and when(r["refresh_expiry"]) - when(RENDER_NOW)
                < datetime.timedelta(days=5)]
        for id in soon:
            days = (when(st["accounts"][id]["refresh_expiry"]) - when(RENDER_NOW)).days
            checks.append({"name": "refresh " + id, "status": "warn", "fix": "signin " + id,
                           "message": "%s's login expires in %s" % (id, plural(days, "day"))})
    last = max(when(r["last_at"]) for r in st["accounts"].values() if r.get("last_at", Z) != Z)
    ago = int((when(RENDER_NOW) - last).total_seconds() // 60)
    checks.append({"name": "usage API", "status": "ok", "fix": None,
                   "message": "reachable; last read %dm ago" % max(ago, 1)})
    saved = when(st.get("saved_at") or RENDER_NOW)
    quiet = int((when(RENDER_NOW) - max(saved, last)).total_seconds() // 60)
    if quiet > 6:  # three of the daemon's two-minute saves (DaemonView.staleAfter)
        checks.append({"name": "daemon", "status": "fail", "fix": "daemon restart",
                       "message": "installed, but it has not polled for %dm" % quiet})
    else:
        checks.append({"name": "daemon", "status": "ok", "fix": None, "message": "installed and polling"})
    if mixed:
        checks += [
            {"name": "status line", "status": "fail", "fix": "statusline install",
             "message": "Claude Code's settings.json has no status line"},
            {"name": "plugin", "status": "warn", "fix": "plugin update",
             "message": "the cs plugin is one release behind"}]
    else:
        checks.append({"name": "status line", "status": "ok", "fix": None, "message": "installed in Claude Code"})
    return {"checks": checks}


def end_at(series, key, target, k):
    """Ends a reading series at target: its last k readings climb in a line
    from the reading before them, or, when target is lower, from a reset
    to 0, as a window does. A gap (no reading) stays one, but the last
    reading is always there."""
    tail = series[-k:]
    before = next((p[key] for p in reversed(series[:-k]) if p[key] is not None), 0)
    start = before if target >= before else 0
    for i, p in enumerate(tail):
        if p[key] is None and i < len(tail) - 1:
            continue
        p[key] = round(start + (target - start) * (i + 1) / len(tail), 1)


def history(files):
    """The test fixture's 30 days, told about the set's own accounts
    (QA 2026-10-28: every switch was credited to default, between accounts
    default cannot use, its reasons named accounts the set lacks, and its
    week figures were not the ones Accounts showed). Each switch goes to
    the profile whose pool holds the account switched to, from that
    profile's account before it; each account's series ends at its
    state.json reading."""
    st = files["state.json"]
    ids = list(st["accounts"].keys())
    profs = files["cli-profile-list.json"]["profiles"]
    h = base("cli-history-usage.json")
    rename = {a["id"]: ids[i] for i, a in enumerate(h["accounts"]) if i < len(ids)}
    h["accounts"] = [dict(a, id=rename[a["id"]]) for a in h["accounts"] if a["id"] in rename]
    for a in h["accounts"]:
        u = st["accounts"][a["id"]].get("last_usage") or {}
        # Readings are two hours apart: the session over its last 6h, the
        # week over its last day.
        for key, k in (("five_hour", 3), ("seven_day", 12)):
            if (u.get(key) or {}).get("utilization") is not None:
                end_at(a["series"], key, u[key]["utilization"], k)
    live, out = {}, []
    for w in h["switches"]:
        to = rename.get(w["to"])
        holders = [p for p in profs if to in p["pool"]]
        if not holders:
            continue
        frm = rename.get(w["from"])
        p = next((p for p in holders if frm in p["pool"]), holders[0])
        if frm not in p["pool"] or frm == to:
            frm = live.get(p["name"]) or next((a for a in p["pool"] if a != to), None)
        if frm is None or frm == to:
            continue
        reason = re.sub(r"(?<![\w-])" + re.escape(w["from"]) + r"(?![\w-])", frm, w["reason"])
        out.append(dict(w, **{"from": frm, "to": to, "profile": p["name"], "reason": reason}))
        live[p["name"]] = to
    h["switches"] = out
    return h


def agree(files, mixed):
    """The files a set would otherwise borrow from the test fixtures, made to
    agree with its own story (QA 2026-10-28: a docs set's Rotation pane said
    90/95 beside cards switching at 85/98, Health failed four checks beside
    an Overview with nothing to do, History showed accounts the set lacks)."""
    st = files["state.json"]
    profs = files["cli-profile-list.json"]["profiles"]
    th = profs[0]["thresholds"]
    if "config.json" not in files:
        c = base("config.json")
        c["switch_at"], c["switch_at_weekly"] = str(th["switch_at"]), str(th["switch_at_weekly"])
        c["hard_floor"] = str(th["hard_floor"])
        c["poll_active"] = "3m0s"  # inside its range (at least 2m)
        files["config.json"] = c
    if "cli-doctor.json" not in files:
        files["cli-doctor.json"] = doctor(files, mixed)
    if "cli-history-usage.json" not in files:
        files["cli-history-usage.json"] = history(files)
    if "cli-daemon-status.json" not in files:
        d = base("cli-daemon-status.json")
        d["mode"], d["daemon_live"] = ("live" if st.get("daemon_live") else "dry-run"), bool(st.get("daemon_live"))
        files["cli-daemon-status.json"] = d


def write(name, files, mixed=False):
    if "cli-pool-list.json" not in files:
        own_pools(files)
    agree(files, mixed)
    d = os.path.join(OUT, name)
    if os.path.exists(d):
        shutil.rmtree(d)
    os.makedirs(d)
    for n, v in files.items():
        with open(os.path.join(d, n), "w") as f:
            json.dump(v, f, indent=1, sort_keys=True)
            f.write("\n")


LONG = "research-team-extremely-long-account-name"
LONGP = "client-example-enterprise-integration"
work = ["w%02d" % i for i in range(1, 13)]
accounts = {a: rec(a, 5 * i % 100, 7 * i % 100) for i, a in enumerate(work)}
accounts["w01"] = rec("w01", 62, 18, models=[("Modelone", 71), ("Modeltwo", 12),
                                             ("Modelthree Extended Thinking Preview", 93)])
accounts["w03"] = rec("w03", 100, 50, burnt="2026-10-08T03:26:41Z")
accounts["w04"] = rec("w04", 3, 4, err="usage API: 401 Unauthorized")
accounts["w05"] = rec("w05", 8, 9, err="usage API: timeout")
# A login that renews in three days: Health warns about it.
accounts["w07"]["refresh_expiry"] = "2026-10-11T08:46:41Z"
accounts["personal"] = rec("personal", 14, 69)
accounts["x-unpooled"] = rec("x-unpooled", 0, 1)
accounts[LONG] = rec(LONG, 33, 44)
accounts["old-sso"] = rec("old-sso", 0, 0, err="no stored credential")
state = {"version": 1, "accounts": accounts,
         "profiles": {"default": {"active_account": "personal", "last_switch": NOW, "active_at": NOW},
                      "work": {"active_account": "w01", "last_switch": NOW, "active_at": NOW},
                      "review": {"active_account": "old-sso", "pinned": "old-sso", "last_switch": Z, "active_at": Z},
                      LONGP: {"active_account": LONG, "last_switch": Z, "active_at": Z}},
         "emails": {"w01": "person-1@example.com",
                    LONG: "someone.with.a.really.long.address@enterprise-client.example.com"},
         "daemon_live": True, "daemon_since": "2026-10-08T01:00:00Z", "daemon_build_time": Z, "saved_at": NOW}
plist = {"profiles": [
    prof("default", ["personal", "x-unpooled"], ["personal"], live="personal"),
    prof("work", work, work, dir="~/.claude-work", live="w01", sa=75, sw=90,
         ov={"switch_at": "75", "switch_at_weekly": "90"}),
    prof("review", ["old-sso"], ["old-sso"], dir="~/.claude-review", live="old-sso", pinned="old-sso", signed="no"),
    prof(LONGP, [LONG], [LONG], dir="/Users/example/Projects/clients/example/very/deep/path/.claude-enterprise",
         live=LONG, signed="unknown"),
    prof("scratch", [], [], dir="~/.claude-scratch", signed="no")],
    "ghosts": [{"profile": "old", "account": "w05", "why": "removed", "since": "2026-10-01T09:00:00Z"},
               {"profile": "legacy-" + LONGP, "account": LONG, "why": "repointed", "since": None}]}
why = {"dir": "/Users/example", "profiles": [
    why_prof("default", ["personal", "x-unpooled"], "personal",
             {"kind": "stay", "reason": "active account at 69%, under the 95% trigger"}, best="x-unpooled"),
    why_prof("work", work, "w01",
             {"kind": "switch", "target": "w02",
              "reason": "w01's Modelthree Extended Thinking Preview weekly limit is at 93%, over the 90% trigger, "
                        "and w02 has room on both windows"}, best="w02", sa=75, sw=90),
    why_prof("review", ["old-sso"], "old-sso",
             {"kind": "stay", "reason": "pinned to old-sso; automatic rotation is off"},
             best_why="review has no other account"),
    why_prof(LONGP, [LONG], LONG, {"kind": "wait", "reason": "only account in the pool"},
             best_why=LONGP + " has no other account"),
    why_prof("scratch", [], None, {"kind": "stay", "reason": "no account in the pool"},
             best_why="scratch has no accounts")]}
alist = {"accounts": [info(a, "work", "work" if a == "w01" else None) for a in work] + [
    info("personal", "default", "default", plan=None),
    info("x-unpooled", "default", plan="Pro"),
    info(LONG, LONGP, LONGP, plan="Max 5x (team, enterprise seat)",
         email="someone.with.a.really.long.address@enterprise-client.example.com"),
    info("old-sso", "review", "review", state="needs_login")]}
write("many", {"state.json": state, "cli-profile-list.json": plist, "why-profiles.json": why,
               "cli-account-list.json": alist}, mixed=True)  # its Health pictures failing checks and Fix

# No [[profile]] blocks: the implicit default profile.
s1 = dict(state)
s1["profiles"] = {"default": {"active_account": "personal", "last_switch": NOW, "active_at": NOW}}
s1["accounts"] = {k: state["accounts"][k] for k in ("personal", "x-unpooled", "w01")}
pool1 = ["personal", "x-unpooled", "w01"]
write("one-profile", {
    "state.json": s1,
    "cli-profile-list.json": {"profiles": [prof("default", pool1, [], from_env=True, declared=False,
                                                live="personal")], "ghosts": []},
    "why-profiles.json": {"dir": "/Users/example", "profiles": [
        why_prof("default", pool1, "personal",
                 {"kind": "stay", "reason": "active account at 69%, under the 95% trigger"}, best="x-unpooled")]},
    "cli-account-list.json": {"accounts": [info(a, "default") for a in pool1]}})

# The README's hero: two healthy profiles, nothing wrong anywhere.
hero_accounts = {
    "work-1": rec("work-1", 18, 41),
    "personal": rec("personal", 7, 22),
    "work-team": rec("work-team", 6, 12),
    "work-2": rec("work-2", 3, 9),
}


def hero_why(name, pool, active, reason, best):
    accts = []
    for a in pool:
        u = hero_accounts[a]["last_usage"]
        fh, sd = u["five_hour"]["utilization"], u["seven_day"]["utilization"]
        accts.append({"id": a, "active": a == active, "eligible": True, "utilization": max(fh, sd),
                      "window": "seven_day" if sd >= fh else "five_hour",
                      "why": "at %d%%, ready" % max(fh, sd)})
    return {"profile": name, "pool": pool, "accounts": accts,
            "decision": {"kind": "stay", "reason": reason},
            "current": name == "default", "best": best, "best_why": None,
            "thresholds": {"switch_at": 85, "switch_at_weekly": 98, "hard_floor": 99, "landing_margin": 10}}


hero_prof = [prof("default", ["work-1", "personal"], ["work-1", "personal"], live="work-1", sa=85, sw=98),
             prof("work", ["work-team", "work-2"], ["work-team", "work-2"], dir="~/.claude-work",
                  live="work-team", sa=85, sw=98)]
for p in hero_prof:
    p["thresholds"]["hard_floor"] = 99
write("hero", {
    "state.json": {"version": 1, "accounts": hero_accounts,
                   "profiles": {"default": {"active_account": "work-1", "last_switch": NOW, "active_at": NOW},
                                "work": {"active_account": "work-team", "last_switch": NOW, "active_at": NOW}},
                   "emails": {"work-1": "person1@example.com", "work-team": "person2@example.com"},
                   "daemon_live": True, "daemon_since": "2026-10-08T01:00:00Z", "daemon_build_time": Z,
                   "saved_at": NOW},
    "cli-profile-list.json": {"profiles": hero_prof, "ghosts": []},
    "why-profiles.json": {"dir": "/Users/example", "profiles": [
        hero_why("default", ["work-1", "personal"], "work-1",
                 "active account at 41%, under the 98% trigger", "personal"),
        hero_why("work", ["work-team", "work-2"], "work-team",
                 "active account at 12%, under the 98% trigger", "work-2")]},
    "cli-account-list.json": {"accounts": [
        info("work-1", "default", "default", email="person1@example.com"),
        info("personal", "default", plan="Pro"),
        info("work-team", "work", "work", plan="Team", email="person2@example.com"),
        info("work-2", "work", plan="Max 5x")]}})

# The tutorial (docs/TUTORIAL.md): the cs CLI screenshots' story, a moment
# later. default runs personal with research refused until its session
# resets; work has just rotated from work-1 (over its 85% session trigger)
# to work-team, and work's Chrome profile ("Work") is still signed in as
# work-1, so the work card asks to sign in again.
IN37M = "2026-10-08T03:22:41Z"
tut_accounts = {
    "personal": rec("personal", 24, 44),
    "research": rec("research", 100, 71, burnt=IN37M),
    "work-1": rec("work-1", 88, 63),
    "work-team": rec("work-team", 7, 22),
}


def tut_why(name, pool, active, reason, refused):
    accts = []
    for a in pool:
        u = tut_accounts[a]["last_usage"]
        fh, sd = u["five_hour"]["utilization"], u["seven_day"]["utilization"]
        accts.append({"id": a, "active": a == active, "eligible": a not in refused,
                      "utilization": max(fh, sd), "window": "seven_day" if sd >= fh else "five_hour",
                      "why": refused.get(a, "at %d%%, ready" % max(fh, sd))})
    return {"profile": name, "pool": pool, "accounts": accts,
            "decision": {"kind": "stay", "reason": reason},
            "current": name == "default", "best": None, "best_why": None,
            "thresholds": {"switch_at": 85, "switch_at_weekly": 98, "hard_floor": 99, "landing_margin": 10}}


tut_prof = [prof("default", ["personal", "research"], ["personal", "research"], live="personal", sa=85, sw=98),
            prof("work", ["work-1", "work-team"], ["work-1", "work-team"], dir="~/.claude-work",
                 live="work-team", sa=85, sw=98)]
for p in tut_prof:
    p["thresholds"]["hard_floor"] = 99
    p["chrome"], p["chrome_name"] = ("Profile 1", "Work") if p["name"] == "work" else (None, None)
write("tutorial", {
    "state.json": {"version": 1, "accounts": tut_accounts,
                   "profiles": {"default": {"active_account": "personal", "last_switch": NOW, "active_at": NOW},
                                "work": {"active_account": "work-team", "last_switch": NOW, "active_at": NOW}},
                   "emails": {"personal": "person1@example.com", "work-team": "person2@example.com"},
                   "daemon_live": True, "daemon_since": "2026-10-08T01:00:00Z", "daemon_build_time": Z,
                   "saved_at": NOW},
    "cli-profile-list.json": {"profiles": tut_prof, "ghosts": []},
    "why-profiles.json": {"dir": "/Users/example", "profiles": [
        tut_why("default", ["personal", "research"], "personal",
                "active account at 44%, under the 98% trigger",
                {"research": "refused on its five_hour window"}),
        tut_why("work", ["work-1", "work-team"], "work-team",
                "active account at 22%, under the 98% trigger",
                {"work-1": "at 88%, over the 85% session trigger"})]},
    "cli-account-list.json": {"accounts": [
        info("personal", "default", "default", plan="Max 5x", email="person1@example.com"),
        info("research", "default", plan="Pro"),
        info("work-1", "work", plan="Max 20x"),
        info("work-team", "work", "work", plan="Team", email="person2@example.com")]},
    # Read by --render only when present (App.swift's Render).
    "render-chrome-list.json": {
        "supported": True,
        "chrome_profiles": [
            {"account": "personal", "added": "2026-10-07T12:00:00Z", "existing": False,
             "live_in": ["default"], "name": None, "profile_dir": "claudeswitch-personal"},
            {"account": "research", "added": "2026-10-07T12:00:00Z", "existing": True,
             "live_in": [], "name": "Research", "profile_dir": "Profile 2"}],
        "live": [
            {"account": "personal", "last_from": None, "last_switch": None, "name": None,
             "profile": "default", "profile_dir": "claudeswitch-personal", "rule": "account"},
            {"account": "work-team", "last_from": "work-1", "last_switch": NOW, "name": "Work",
             "profile": "work", "profile_dir": "Profile 1", "rule": "profile"}]},
    "render-chrome-profiles.json": {
        "supported": True, "local_state": True, "last_used": "Default",
        "chrome_profiles": [
            {"folder": "Default", "in_chrome": True, "last_used": True, "name": "Person 1",
             "used_by_accounts": [], "used_by_profiles": []},
            {"folder": "Profile 1", "in_chrome": True, "last_used": False, "name": "Work",
             "used_by_accounts": [], "used_by_profiles": ["work"]},
            {"folder": "Profile 2", "in_chrome": True, "last_used": False, "name": "Research",
             "used_by_accounts": ["research"], "used_by_profiles": []},
            {"folder": "claudeswitch-personal", "in_chrome": True, "last_used": False, "name": None,
             "used_by_accounts": ["personal"], "used_by_profiles": []}]}})

# Shared pools (docs/PROFILES.md §10): personal is default's first pool,
# ops work's first; both list team second; spare is empty and no profile
# lists it; loose-1 is in no pool (default's, last). team-2 is live in work,
# so in default it is in use by work and not offered.
pool_accounts = {
    "personal": rec("personal", 14, 38),
    "team-1": rec("team-1", 3, 4, err="usage API: 401 Unauthorized"),
    "team-2": rec("team-2", 41, 55),
    "team-3": rec("team-3", 6, 12),
    "ops-1": rec("ops-1", 9, 31),
    "loose-1": rec("loose-1", 0, 2),
}
team = ["team-1", "team-2", "team-3"]
shared = [
    {"name": "personal", "accounts": ["personal"], "profiles": ["default"], "own": False},
    {"name": "team", "accounts": team, "profiles": ["default", "work"], "own": False},
    {"name": "ops", "accounts": ["ops-1"], "profiles": ["work"], "own": False},
    {"name": "spare", "accounts": [], "profiles": [], "own": False}]
pool_prof = [prof("default", ["personal"] + team + ["loose-1"], ["personal"] + team, live="personal", sa=85, sw=98),
             prof("work", ["ops-1"] + team, ["ops-1"] + team, dir="~/.claude-work", live="team-2", sa=85, sw=98)]
pool_prof[0]["pools"], pool_prof[1]["pools"] = ["personal", "team"], ["ops", "team"]


def pool_why(name, pool, active, reason, elsewhere):
    accts = []
    for a in pool:
        u = pool_accounts[a]["last_usage"]
        fh, sd = u["five_hour"]["utilization"], u["seven_day"]["utilization"]
        why = "in use by " + elsewhere[a] if a in elsewhere else "at %d%%, ready" % max(fh, sd)
        if a == "team-1":
            why = "needs a sign-in"
        accts.append({"id": a, "active": a == active, "eligible": a not in elsewhere and a != "team-1",
                      "utilization": max(fh, sd), "window": "seven_day" if sd >= fh else "five_hour",
                      "why": why, "active_in": elsewhere.get(a)})
    return {"profile": name, "pool": pool, "accounts": accts,
            "decision": {"kind": "stay", "reason": reason},
            "current": name == "default", "best": None, "best_why": None,
            "thresholds": {"switch_at": 85, "switch_at_weekly": 98, "hard_floor": 99, "landing_margin": 10}}


def pinfo(id, pool, profiles, active_in=None, plan="Team", email=None, state="available"):
    i = info(id, profiles[0] if profiles else None, active_in, plan=plan, email=email, state=state)
    i["pool"], i["profiles"] = pool, profiles
    return i


write("pools", {
    "state.json": {"version": 1, "accounts": pool_accounts,
                   "profiles": {"default": {"active_account": "personal", "last_switch": NOW, "active_at": NOW},
                                "work": {"active_account": "team-2", "last_switch": NOW, "active_at": NOW}},
                   "emails": {"personal": "person1@example.com", "team-2": "person2@example.com"},
                   "daemon_live": True, "daemon_since": "2026-10-08T01:00:00Z", "daemon_build_time": Z,
                   "saved_at": NOW},
    "cli-profile-list.json": {"profiles": pool_prof, "ghosts": [], "pools": shared},
    "cli-pool-list.json": {"pools": shared, "unlisted": ["loose-1"]},
    "why-profiles.json": {"dir": "/Users/example", "profiles": [
        dict(pool_why("default", ["personal"] + team + ["loose-1"], "personal",
                      "active account at 38%, under the 98% trigger", {"team-2": "work"}), best="team-3"),
        dict(pool_why("work", ["ops-1"] + team, "team-2",
                      "active account at 55%, under the 98% trigger", {}), best="ops-1")]},
    "cli-account-list.json": {"accounts": [
        pinfo("personal", "personal", ["default"], "default", plan="Max 20x", email="person1@example.com"),
        pinfo("team-1", "team", ["default", "work"], state="needs_login"),
        pinfo("team-2", "team", ["default", "work"], "work", email="person2@example.com"),
        pinfo("team-3", "team", ["default", "work"]),
        pinfo("ops-1", "ops", ["work"], plan="Max 20x"),
        pinfo("loose-1", None, ["default"], plan="Pro")]}})

# A binary from before shared pools: the hero's picture, with none of the
# pool keys, and `pool list` refused as an unknown command would be.
write("older-cli", {
    "state.json": {"version": 1, "accounts": hero_accounts,
                   "profiles": {"default": {"active_account": "work-1", "last_switch": NOW, "active_at": NOW},
                                "work": {"active_account": "work-team", "last_switch": NOW, "active_at": NOW}},
                   "emails": {"work-1": "person1@example.com", "work-team": "person2@example.com"},
                   "daemon_live": True, "daemon_since": "2026-10-08T01:00:00Z", "daemon_build_time": Z,
                   "saved_at": NOW},
    "cli-profile-list.json": {"profiles": [prof("default", ["work-1", "personal"], ["work-1", "personal"],
                                                live="work-1", sa=85, sw=98),
                                           prof("work", ["work-team", "work-2"], ["work-team", "work-2"],
                                                dir="~/.claude-work", live="work-team", sa=85, sw=98)],
                              "ghosts": []},
    "cli-pool-list.json": {"error": {"code": "usage", "message": "unknown command \"pool\"", "hint": ""}},
    "why-profiles.json": {"dir": "/Users/example", "profiles": [
        hero_why("default", ["work-1", "personal"], "work-1",
                 "active account at 41%, under the 98% trigger", "personal"),
        hero_why("work", ["work-team", "work-2"], "work-team",
                 "active account at 12%, under the 98% trigger", "work-2")]},
    "cli-account-list.json": {"accounts": [
        info("work-1", "default", "default", email="person1@example.com"),
        info("personal", "default", plan="Pro"),
        info("work-team", "work", "work", plan="Team", email="person2@example.com"),
        info("work-2", "work", plan="Max 5x")]}})


# The states no other set draws (QA 2026-10-28), as one story at a moment
# the daemon has been quiet for 21m (its readings are all that old):
#   default  research refused on its session, clearing in 36m, and personal
#            over its own trigger: nothing else is usable, so Next: wait,
#            on the card the menu bar follows
#   work     its pin on work-2 was lifted 41m ago when work-2 was refused
#            (refused for another 1h 10m); rotation moved it to work-team,
#            which reaches its session trigger in 40m, so the pool runs dry
#            then (before work-team's session resets in 2h)
#   lab      pinned hard to lab-1, which was refused: it stays; lab-2 is the
#            best, short of the landing margin (the amber warning)
#   client   pinned softly to client-1, already on its best (client-2 has
#            less room), its pool running dry on Saturday; its last read
#            timed out, and its session window reset since the one before
AGO21M = "2026-10-08T02:25:41Z"
AGO10M = "2026-10-08T02:36:41Z"
LIFTED = "2026-10-08T02:05:41Z"
IN36M = "2026-10-08T03:22:41Z"
IN40M = "2026-10-08T03:26:41Z"
IN70M = "2026-10-08T03:56:41Z"
IN85M = "2026-10-08T04:11:41Z"
SATURDAY = "2026-10-10T21:00:00Z"
TIMEOUT = ('calling usage API: Get "https://api.anthropic.com/api/oauth/usage": context deadline exceeded '
           '(Client.Timeout exceeded while awaiting headers)')
st_accounts = {
    "research": rec("research", 100, 71, burnt=IN36M, fh_reset=IN36M, last=AGO21M),
    "personal": rec("personal", 91, 35, last=AGO21M),
    "work-team": rec("work-team", 72, 30, last=AGO21M),
    "work-2": rec("work-2", 100, 50, burnt=IN70M, fh_reset=IN70M, last=AGO21M),
    "lab-1": rec("lab-1", 100, 64, burnt=IN85M, fh_reset=IN85M, last=AGO21M),
    "lab-2": rec("lab-2", 79, 20, last=AGO21M),
    "client-1": rec("client-1", 80, 30, err=TIMEOUT, fh_reset=AGO10M, last=AGO21M),
    "client-2": rec("client-2", 5, 75, last=AGO21M),
}
ST_POOLS = {"default": ["research", "personal"], "work": ["work-team", "work-2"],
            "lab": ["lab-1", "lab-2"], "client": ["client-1", "client-2"]}
ST_LIVE = {"default": "research", "work": "work-team", "lab": "lab-1", "client": "client-1"}
ST_PINNED = {"lab": "lab-1", "client": "client-1"}
st_prof = [prof(n, ST_POOLS[n], ST_POOLS[n], dir=None if n == "default" else "~/.claude-" + n,
                live=ST_LIVE[n], pinned=ST_PINNED.get(n), sa=85, sw=98) for n in ST_POOLS]
for p in st_prof:
    p["thresholds"]["hard_floor"] = 99
NO_ROOM = "no other account in %s has room (each is over its trigger, refused, reserved or unread)"


def st_verdict(a, active):
    u = st_accounts[a]["last_usage"]
    fh, sd = u["five_hour"]["utilization"], u["seven_day"]["utilization"]
    burnt = st_accounts[a]["burnt_until"] != Z
    session = fh - 85 >= sd - 98  # the window nearer its own trigger binds
    worst, label, trig = (fh, "session", 85) if session else (sd, "week", 98)
    if burnt:
        why = "refused on its session window"
    elif worst >= trig:
        why = "at %d%% of its %s, over the %d%% trigger" % (worst, label, trig)
    else:
        why = "at %d%% of its %s, %d points of room" % (worst, label, trig - worst)
    return {"id": a, "active": a == active, "eligible": not burnt and worst < trig, "utilization": worst,
            "window": "five_hour" if session else "seven_day", "why": why}


def st_why(name, decision, best=None, best_why=None, on_best=False, dry=None, trigger=None):
    accts = [st_verdict(a, ST_LIVE[name]) for a in ST_POOLS[name]]
    for v in accts:
        if trigger and v["id"] == trigger[0]:
            v["trigger_at"] = trigger[1]
    return {"profile": name, "pool": ST_POOLS[name], "accounts": accts, "decision": decision,
            "current": name == "default", "best": best, "best_why": best_why, "on_best": on_best,
            "pool_dry_at": dry,
            "thresholds": {"switch_at": 85, "switch_at_weekly": 98, "hard_floor": 99, "landing_margin": 10}}


def st_info(id, profile, plan="Max 20x", email=None):
    i = info(id, profile, profile if ST_LIVE[profile] == id else None, plan=plan, email=email)
    i["pinned"] = ST_PINNED.get(profile) == id
    i["pin_hard"] = id == "lab-1"
    return i


write("states", {
    "state.json": {"version": 1, "accounts": st_accounts,
                   "profiles": {n: dict({"active_account": ST_LIVE[n], "active_at": NOW,
                                         "last_switch": LIFTED if n == "work" else NOW},
                                        **({"pinned": ST_PINNED[n]} if n in ST_PINNED else {}))
                                for n in ST_POOLS},
                   "emails": {"research": "person1@example.com", "work-team": "person2@example.com"},
                   "daemon_live": True, "daemon_since": "2026-10-08T01:00:00Z", "daemon_build_time": Z,
                   "saved_at": AGO21M},
    "cli-profile-list.json": {"profiles": st_prof, "ghosts": []},
    "why-profiles.json": {"dir": "/Users/example", "profiles": [
        st_why("default", {"kind": "wait", "reason": "active account was refused and nothing else is usable"},
               best_why=NO_ROOM % "default"),
        st_why("work", {"kind": "stay", "reason": "active account at 72% of its session, under the 85% trigger"},
               best_why=NO_ROOM % "work", dry=IN40M, trigger=("work-team", IN40M)),
        st_why("lab", {"kind": "stay", "reason": "pinned to lab-1; automatic rotation is off"}, best="lab-2",
               best_why="lab-2's session window has only 6 points below its 85% trigger, "
                        "short of the 10-point landing margin"),
        st_why("client", {"kind": "stay", "reason": "pinned to client-1; automatic rotation is off"},
               best="client-2", on_best=True, dry=SATURDAY)]},
    "cli-account-list.json": {"accounts": [
        st_info("research", "default", email="person1@example.com"),
        st_info("personal", "default", plan="Pro"),
        st_info("work-team", "work", plan="Team", email="person2@example.com"),
        st_info("work-2", "work", plan="Max 5x"),
        st_info("lab-1", "lab"),
        st_info("lab-2", "lab", plan="Max 5x"),
        st_info("client-1", "client", plan="Team"),
        st_info("client-2", "client", plan="Team")]}})
with open(os.path.join(OUT, "states", "render-audit.jsonl"), "w") as f:
    f.write(json.dumps({"kind": "unpin", "profile": "work", "account": "work-2", "at": LIFTED,
                        "reason": "it was refused"}, sort_keys=True) + "\n")
