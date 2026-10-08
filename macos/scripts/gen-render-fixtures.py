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

    python3 macos/scripts/gen-render-fixtures.py
"""
import json
import os
import shutil

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "..", "RenderFixtures")
NOW = "2026-10-08T02:45:41Z"
Z = "0001-01-01T00:00:00Z"
IN2H = "2026-10-08T04:46:41Z"
IN3D = "2026-10-11T02:46:41Z"
SEAT = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa@11111111-1111-1111-1111-111111111111"


def rec(id, fh, sd, err=None, burnt=None, models=(), last=NOW):
    limits = [
        {"kind": "session", "group": "session", "percent": fh, "severity": "normal",
         "is_active": False, "resets_at": IN2H, "scope": None},
        {"kind": "weekly_all", "group": "weekly", "percent": sd, "severity": "normal",
         "is_active": True, "resets_at": IN3D, "scope": None},
    ]
    for m, p in models:
        limits.append({"kind": "weekly_scoped", "group": "weekly", "percent": p, "severity": "normal",
                       "is_active": False, "resets_at": IN3D,
                       "scope": {"model": {"id": None, "display_name": m}, "surface": None}})
    r = {"id": id, "org_id": "11111111-1111-1111-1111-111111111111", "seat": SEAT,
         "last_usage": {"five_hour": {"utilization": fh, "resets_at": IN2H},
                        "seven_day": {"utilization": sd, "resets_at": IN3D},
                        "limits": limits},
         "last_at": last, "prev_at": Z, "burnt_until": burnt or Z,
         "refresh_expiry": "2026-11-08T02:45:41Z", "token_expiry": Z}
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


def write(name, files):
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
    prof("work", work, work, dir="~/.claude-work", live="w01", sa=75, sw=98,
         ov={"switch_at": "75", "switch_at_weekly": "98"}),
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
                        "and w02 has 88 points of room on both windows"}, best="w02", sa=75, sw=98),
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
               "cli-account-list.json": alist})

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
