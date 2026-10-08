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
