# The app's CLI contract (IMPROVEMENTS M6)

Every action the menu-bar app offers has a `claudeswitch` form that takes
`--json`, never prompts, and answers with exactly one JSON object on stdout.
The app never parses human text and never touches the keychain, the usage
API or launchd itself: it runs these commands (M1, M3).

Built in lane 12. Within a contract version it is additive only: a key or
an error code is never renamed or removed; new ones may appear, and readers
ignore what they do not know.

**Contract 2 (lane 16)** removes account scope (the accounts' `scope` keys,
`account scope`, `--scope`) — the one breaking change, by owner decision S1 (IMPROVEMENTS) —
and adds `version --json`, `config clean`, `add --from` and `why --json`'s
`best`. The app checks `version --json`'s `contract` and refuses a binary
below the one it was built for; a binary that does not answer
`version --json` with that object (0.5.0 and every build before lane 16) is
too old.

## version

`claudeswitch version --json` → `{"version": "0.5.1", "contract": 2}`.
`version` is the build (`dev` for a source build); `contract` the version of
this document's contract. Plain `claudeswitch version` still prints
`claudeswitch <version>`.

## Conventions

- **Success**: exit status 0, one JSON object on stdout. Human notes (and
  the human form of a command run with `--json`) go to stderr.
- **Failure**: exit status 1, and on stdout:

  ```json
  {"error": {"code": "live", "message": "account \"a1\" is or may be live in profile \"work\" …", "hint": "switch profile work to another account first: …", "retry_at": null}}
  ```

  `code` is one of the table below; `message` is the sentence a person
  reads; `hint` is what to do next, `""` when there is nothing to add.
  Branch on `code`, show `message` and `hint`.
- **`retry_at`** (contract 2, additive, lane 17): an error object may also
  carry `retry_at`, an RFC 3339 time, when the refusal is expected to clear
  by then. Today only `live` carries it: the §3 check could not confirm the
  account is absent from another profile because a rate-limit lock on the
  token it needed refused the identity lookup (R1). The message then reads
  `can't confirm "a1" isn't signed in under profile "work": the check is
  rate limited until 14:03:07; try again then` (local wall-clock time).
  It is `null` on every other error.
- **No prompts**: `--json` (or `--yes`) turns every prompt off. A command
  that would have asked refuses with `confirmation_required` (pass `--yes`)
  or `name_required` instead.
- **Absent values** are `null`, never `""` or a missing key, in the
  objects this document lists.
- Times are RFC 3339 (UTC where this document says so).
- `--config PATH` is accepted by every command, as everywhere else.
- A wrong flag on a `--json` command line is a `usage` error object too
  (exit 1), on every command, not usage text.

### Error codes

| code | meaning |
|---|---|
| `usage` | bad arguments or flags |
| `not_found` | no such account, profile, setting, slot or guard |
| `invalid_value` | a value the validator refuses (the hint says what is accepted) |
| `config_invalid` | the config does not load, or an edit would not; nothing was written |
| `no_config` | there is no config file to edit (`claudeswitch setup` first) |
| `live` | the account is, or may be (D18: unknown counts as live), live in a profile or a removed profile's old item; the message names it; `retry_at` when a rate-limit lock is why it could not be confirmed |
| `in_other_pool` | D1: another profile's pool lists the account |
| `outside_pool` | D5: the account is not in this profile's pool |
| `would_orphan` | removing it from its pool would leave it in no pool |
| `not_vaulted` | no credential is stored under that id |
| `daemon_running` | the command needs the daemon stopped (`daemon stop`); `account rename` only |
| `confirmation_required` | a prompt would be needed: pass `--yes` |
| `name_required` | `add` with no name and nobody to ask; `hint` is the suggested name alone (may be `""`) |
| `not_installed` | the daemon service is not installed |
| `unsupported_platform` | neither launchd nor systemd |
| `service_failed` | launchctl or systemctl failed; `hint` is its output |
| `not_active` | `pin`: the account is not the one live in its profile |
| `no_pending_login` | `login --code` with no `--direct` login started, or it expired |
| `wrong_account` | the login (or the live credential, for `add`) is another seat; nothing stored |
| `already_vaulted` | that credential is already vaulted under another name |
| `daemon_not_loaded` | a seed, or an account delete, timed out waiting for the running daemon to load the edited config |
| `config_changed` | a command could not undo its own config edit because something else changed the config meanwhile; that change is kept (and so is the credential) |
| `last_profile` | `profile remove`: the only profile (or the implicit one, with no `[[profile]]` blocks) cannot be removed |
| `binary_not_durable` | `daemon install` from a temporary, translocated or `go run` path |
| `exists` | `init`: a config is already at that path; nothing was written |
| `failed` | anything else; `message` says what |

All config edits are textual: only the lines concerned change (comments and
layout survive), the result is parsed back and checked before it replaces
the file, and the file's mode and symlink are kept. A running daemon picks
every config edit up without a restart (D21).

## config

`claudeswitch config --json` — every setting's value, as strings:

```json
{"path": "/…/config.toml", "switch_at": "85", "poll_hot": "1m0s", "models": "", …,
 "chrome": {"default": null, "work": "Profile 1"}}
```

`chrome` (C2, additive) is the one value that is not a string: each
profile's Chrome profile folder, `null` for Chrome's last used. It has no
global value and is not in `config schema`.

`claudeswitch config schema --json` — every setting `cs config` knows,
generated from the same table, in its order:

```json
{"settings": [
  {"key": "switch_at", "type": "percent", "default": "85", "min": 0, "max": 100,
   "min_exclusive": true, "enum": [], "scopes": ["global", "profile"],
   "description": "rotate away at this much of the 5-hour window"}, …]}
```

`type` is `percent`, `number`, `int`, `duration` (Go syntax: `90s`, `5m`,
`2h`; `min`/`max` in seconds), `enum` (values in `enum`) or `list`
(comma-separated). `min`/`max` are `null` when unbounded. Cross-setting
rules (`hard_floor` at or above `switch_at`; the poll cadence fitting
`api_budget`) are not in the schema: `set` refuses them with
`invalid_value`. `scopes` contains `profile` for the six keys
`profile set` takes. Numbers must be finite: `NaN` and `Inf` are
`invalid_value`.

`claudeswitch config get <key> [--profile P] --json`:

```json
{"key": "switch_at", "value": "85", "scope": "global"}
{"key": "switch_at", "value": "70", "scope": "profile", "profile": "work", "override": "70"}
```

With `--profile`, `value` is the value in force there and `override` the
profile's own (`null`: it inherits).

`config get chrome --profile P --json` (C2) →
`{"key": "chrome", "value": "Profile 1", "scope": "profile", "profile": "work",
"override": "Profile 1", "name": "Work"}`: `value` is `""` and `override`
`null` when the profile names none (Chrome's last used); `name` is Chrome's
name for the folder, `null` when Local State does not list it. Without
`--profile`, and for `config set chrome`, it is `usage`: chrome is set with
`profile set`. (This `scope` is where a setting is set, global or
per profile; it is not the account scope contract 2 removed.)

`claudeswitch config set <key> <value> --json`:

```json
{"key": "switch_at", "previous": "85", "value": "80", "path": "/…/config.toml", "daemon_running": true}
```

Errors: `not_found` (unknown key), `invalid_value`, `usage`,
`config_invalid`. (`config <key>` and `config <key> <value>` without the
verb still work.)

**`prefer`** (contract 2, additive, IMPROVEMENTS F1) is in `config --json`,
`config get`/`set` and `config schema` (listed right after
`landing_margin`, with the rotation settings; Settings → Rotation):

```json
{"key": "prefer", "type": "enum", "default": "room", "min": null, "max": null,
 "min_exclusive": false, "enum": ["room", "expiring"], "scopes": ["global", "profile"],
 "description": "which eligible account a rotation takes: \"room\", the most room, or \"expiring\", the weekly window resetting soonest with quota unused"}
```

`room` (the default) is the most-room rule as before; `expiring` takes the
eligible account whose weekly window resets soonest while it still has quota
unused, room breaking ties. Eligibility is the same either way. It also
decides `why --json`'s `best`.

A config may still carry account `scope` lines and `[project.…]` tables
from before lane 16. It loads: they are ignored, and commands that load the
config print one warning naming them (the daemon logs it at start and when
an edit changes them; `doctor` shows it). `config set` and every other
single-line edit leave them as written.

`claudeswitch config clean [--yes] --json` removes them, textually (each
table with the comment lines directly above it), parsed back and checked
like every edit:

```json
{"removed": {"scope": ["a1", "w1"], "projects": 2}, "path": "/…/config.toml"}
```

`scope` lists the accounts whose scope line went; `projects` counts the
tables. With nothing to remove it answers `{"removed": {"scope": [],
"projects": 0}, …}`. Without `--yes`: `confirmation_required`, its message
saying what would go. Errors also: `no_config`, `config_invalid`.

## profile

`claudeswitch profile list --json`:

```json
{"profiles": [
  {"name": "work", "dir": "~/.claude-work", "from_env": false, "declared": true,
   "pool": ["w1", "w2"], "listed": ["w1", "w2"], "paths": ["~/src/work/**"],
   "live": "w1", "pinned": null, "signed_in": "yes",
   "overrides": {"switch_at": "75"},
   "thresholds": {"switch_at": 75, "switch_at_weekly": 98, "hard_floor": 99, "landing_margin": 10},
   "chrome": "Profile 1", "chrome_name": "Work",
   "chrome_resolved": {"account": "w1", "profile": "work", "profile_dir": "Profile 1", "name": "Work",
                       "rule": "profile"}}],
 "ghosts": [{"profile": "old", "account": "a3", "why": "removed", "since": "…"}]}
```

`dir` is as written (`null`: Claude Code's default, `CLAUDE_CONFIG_DIR`
unset). `paths` (contract 2, additive, IMPROVEMENTS F4) are the folders the
profile is picked for, as written (globs; `[]` when none; below). `pool` is the effective pool (D6 included); `listed` is what the
config's `pool` lists (lane 15), so an account in `pool` but not `listed`
is `default`'s by D6 alone and moves with `pool add`. `signed_in` is `yes`,
`no` or `unknown` (the lookup failed). `live` is what state records.

C2 (additive): `chrome` is the Chrome profile folder the profile names
(`null`: Chrome's last used), `chrome_name` Chrome's name for it (`null` when
unknown), and `chrome_resolved` the live account's Chrome profile as
`chrome open` would resolve it (the shape is under chrome below), `null` with
no live account. `chrome` is not in `overrides`.

`claudeswitch profile create <name> [--dir PATH] [--pool a,b] [--seed ACCOUNT] --json`
answers with the new profile as `list` reports it, plus `"seeded": "a1"`
or `null`. Errors: `invalid_value` (bad or taken name, dir clash),
`in_other_pool`, `not_found`, `outside_pool` (seed not in the pool), `live`
(seed live elsewhere), `daemon_not_loaded` (the profile is made, not
seeded: retry with `profile seed`), `no_config`.

`claudeswitch profile seed <name> <account> --json` → `{"profile": "work", "seeded": "w1"}`.
Errors as for `create --seed`.

`claudeswitch profile forget <name> --json` → `{"profile": "old", "released": 1}`.
Error: `not_found` (nothing of that profile is guarded).

`claudeswitch profile remove <name> [--to <profile>] --yes --json` (lane 15) —
removes the `[[profile]]` block (a textual edit, parsed back; the comment
lines directly above it go with it):

```json
{"profile": "review", "to": "default", "moved": ["personal"],
 "ghost": {"profile": "review", "account": "personal", "why": "removed", "since": "…"},
 "kept": {"dir": "~/.claude-review", "credential": "Claude Code-credentials-1a2b3c4d"},
 "daemon_running": false, "daemon_loaded": null,
 "pools": {"default": ["work-1", "work-2", "personal"]}}
```

- `moved` is the profile's effective pool; every account in it joins `to`'s
  pool in the same edit. `to` is `--to`, else `default` (D6). With no
  `default` and no `--to`, an enabled account would be in no pool:
  `would_orphan` (disabled ones are left in none, `to` `null`). Removing
  `default` itself while other profiles exist needs `--to` (`usage`): its
  accounts include every one no pool lists, and they all move.
- **Nothing is deleted but the block.** The profile's folder and its
  keychain item (Linux: credential file) stay; `kept` names them as
  recorded (`dir` as written, `null` for the no-dir profile;
  `credential` the item the daemon last resolved, `null` if none was
  recorded).
- **The ghost (D22, D25).** The account last live in the profile may still
  be signed in there, so it is guarded: installed in no other profile until
  that item no longer holds it, or `profile forget <name>`. `ghost` is that
  guard, `null` when nothing was recorded live. With no daemon running the
  command holds the daemon lock throughout and records the ghost itself (as
  a starting daemon would). With one running, its hot reload stops the
  profile's loop and records the ghost (D21); the command waits up to 10 s
  for it to run the edited config: `daemon_loaded` (`null` with no daemon).
  A timeout is not an error — the edit stays, and every CLI check derives the
  ghost from the state's record meanwhile.
- `confirmation_required` without `--yes`: its `message` says where the
  accounts go, that the folder and login stay, and whether an account
  stays guarded — the app's confirmation text. Errors also: `not_found`
  (the profile or `--to`), `usage` (`--to` itself), `last_profile`,
  `config_invalid`.

`claudeswitch profile pool <profile> add|remove <account> [--to <profile>] --json`:

```json
{"account": "a2", "profile": "work", "changed": true, "pools": {"default": ["a1"], "work": ["w1", "a2"]}}
```

`profile` is where the account is now; `pools` every effective pool.
Pools never overlap (D1): `add` of an account another pool lists is
`in_other_pool`, and the hint is the move: `remove … --to <profile>`, one
edit. An account in no pool joins `default` (D6), so `add` may take one
from there, and `remove` (without `--to`) returns one there; with no
`default` profile that is `would_orphan`, and removing from `default`
itself without `--to` is `usage`. `remove default <id> --to <q>` of an
account no pool lists (default's by D6 alone) is the same edit as
`add <q> <id>` (lane 15): nothing is removed from default's pool. An account live — or maybe live (D18) —
in the profile it leaves, or anywhere but the profile it joins, is `live`
(§3). Errors also: `not_found`, `config_invalid`.

`claudeswitch profile set <profile> <key> <value> --json` — a per-profile
override (D4) of `switch_at`, `switch_at_weekly`, `hard_floor`,
`landing_margin`, `prefer` (`room` or `expiring`, F1) or `models`.
`inherit` (or `""`) removes the override;
for `models`, `none` is an empty list (count no model here) and
`a,b` a list:

```json
{"profile": "work", "key": "switch_at", "override": "70", "effective": "70", "path": "/…/config.toml"}
```

Errors: `not_found` (unknown profile, an undeclared implicit profile, a
key that is global only), `invalid_value`.

`claudeswitch profile set <profile> chrome <folder|name|inherit> --json`
(C2): the Chrome profile the profile's accounts use when they have none of
their own. A name resolves to its folder through Chrome's Local State; the
config stores the folder (`chrome = "Profile 1"` in the `[[profile]]`).
`inherit` removes it (Chrome's last used):

```json
{"profile": "work", "key": "chrome", "override": "Profile 1", "effective": "Profile 1", "name": "Work",
 "path": "/…/config.toml"}
```

`effective` is the folder in force (after `inherit`, Chrome's last-used
folder, `null` when Local State cannot be read). Errors: `not_found` (a
name Chrome does not list — the `hint` lists Chrome's profiles; an unknown
or undeclared profile), `invalid_value` (a folder that could be read as a
flag or a path, or a name two Chrome profiles share).

`claudeswitch profile set <profile> paths <a,b|inherit> --json` (F4,
additive): the folders the profile is picked for, written as
`paths = ["a", "b"]` in its `[[profile]]`. A pattern is absolute or from
`~/`, with `*` and `?` within a folder name and `**` for any number of
folders; a folder belongs to a pattern when the pattern matches it or one
of its parents. Two profiles' paths that could match one folder do not load.
`inherit` (or `""`) removes them:

```json
{"profile": "work", "key": "paths", "override": ["~/src/work/**", "~/clients/acme"],
 "effective": ["~/src/work/**", "~/clients/acme"], "path": "/…/config.toml"}
```

`override` is `null` and `effective` `[]` after `inherit`. Errors:
`invalid_value` (a relative pattern, `[`, `\`, `.`/`..`, a `**` inside a
name, or an overlap with another profile's paths — the message names
both), `not_found` (an unknown or undeclared profile).

`claudeswitch profile which [--dir PATH] --json` (F4) — the profile a
folder (default: the working directory) picks, from the config alone:

```json
{"dir": "/Users/person1/src/work/api", "profile": "work", "pattern": "~/src/work/**",
 "rule": "paths", "config_dir": "~/.claude-work", "from_env": false}
```

`rule` is `paths` (`pattern` matched), `default` (none did; `pattern`
`null`) or `none` (none did and there is no `default`: `profile` and
`config_dir` `null`). `config_dir` is the profile's `dir` as written,
`null` without one. `cs run` with no name starts this profile, and
`cs profile hook zsh|bash|fish` prints a shell hook that sets
`CLAUDE_CONFIG_DIR` to it on each change of folder (not an app action).
Errors: `config_invalid`.

## account

`claudeswitch account list --json` (lane 15) — every configured account,
from `config.toml` and `state.json` alone. It never reads the keychain (a
test fails if it runs `security`), so the app may run it on every refresh:

```json
{"accounts": [
  {"id": "work-1", "email": "person1@example.com", "plan": "Max 20x",
   "seat": "aaaaaaaa-…@11111111-…", "enabled": true, "profile": "default", "active_in": "default",
   "pinned": false, "pin_hard": false, "refresh_expires_at": "…", "access_expires_at": "…", "state": "available",
   "reading": {"five_hour": 3, "seven_day": 41, "binding": "seven_day", "at": "…", "error": null,
               "refused_until": null, "refused_window": null}}]}
```

- Order: the enabled accounts in rotation order, then the disabled ones in
  config order (`enabled: false`).
- `email` and `plan` are what state recorded (`emails`, `plans`): the CLI
  records both when it vaults (`add`, `login`, `setup`) or identifies
  (`identify`) an account, and the daemon records the plan a vault entry
  names whenever it reads one to poll. `null` until then.
- `seat` is the config's `account_uuid@org_id` (`null` when unpinned);
  `profile` the pool owner (D6 included); `active_in` the profile state
  records it live in; `pinned` whether a profile is pinned to it;
  `pin_hard` (contract 2, additive, F3) whether such a pin is hard
  (`account pin --hard`), `false` when it is not pinned.
- `refresh_expires_at` / `access_expires_at` are what the daemon last saw.
  `refresh_expires_at` (F5) is read from state.json, never the keychain; the
  daemon records it on every poll of the account, live or vaulted, and the
  CLI when it vaults one. `null` when unknown (never recorded, or a
  credential that does not report it, e.g. one from `login --direct`). The
  app's re-login reminder fires within 5 days of it.
- `state`: `needs_login` (the last error is one only a sign-in cures, or
  the refresh token has expired), `refused` (`reading.refused_until`),
  `available`, `reserved` (over its configured `reserve`) or `unknown`
  (never read, or the reading outlived its window).
- `reading` is `null` when state holds neither a reading nor an error; its
  windows are `null` when unknown.

`claudeswitch account rename <old> <new> --json` → `{"account": "new", "previous": "old"}`.
Errors: `invalid_value` (a bad name), `daemon_running`, `failed`.

`claudeswitch account delete <id> --yes --json` (also `account remove`,
and `claudeswitch remove <id>`) — owner decision M5: deletes the vault
credential, the `[[account]]` block (with the comment lines directly above
it), its `priority` entry, its pool entry and its state records:

```json
{"account": "w1", "seat": "person-1@org-1", "org_id": "org-1", "email": "…",
 "removed": {"credential": true, "config": true, "priority": true, "pool": "work", "state": true},
 "twin": null, "daemon_running": false}
```

Refused, with nothing changed: `live` while the account is or may be live
in any profile or ghost (holder named), unless another vaulted name holds
the identical credential (`twin`: the one repair for that state; a
profile recording the deleted name as live is cleared);
`confirmation_required` without `--yes` (the message names the account and
seat, for the app's own confirmation); `not_found`.

Liveness is checked before the confirmation, again after it, and again
just before the credential is deleted (lane 12 security review):

- **No daemon running**: the daemon lock is held from the first check to
  the end, so no daemon starts midway. Config edit, then the credential,
  then the state.
- **A daemon running** (owner decision: deleting works while it runs): the
  config edit comes first. The command then waits, up to 10 s, until the
  daemon runs exactly the config it wrote: the daemon records the content
  hash of the config it has loaded in state (`daemon_config_hash`, at
  start and on every reload), so a marker from an earlier run or an
  earlier config never counts. Its reload drops the account's record. The
  command then checks liveness once more, and only then deletes the
  credential. An account the config does not name still waits for the
  daemon to run the config as it is. On a timeout (`daemon_not_loaded`) or
  if the account turned live (`live`), the config is put back and the
  credential kept.

The config is also put back if the credential cannot be deleted. Putting
it back is compare-and-swap: only if the file still holds exactly what
this delete wrote. Otherwise the other change stays, the credential is
kept, and the error is `config_changed`.

`profile create --seed` and `profile seed` wait the same way, for the hash
of the config they wrote as well as for the profile.

`claudeswitch account pin <id> [--hard] --json` → `{"profile": "work", "pinned": "w1", "pin_hard": false}`.
Suspends automatic rotation in the profile the account belongs to. Only the
account live there can be pinned (`not_active` otherwise, and for a disabled
account no profile's pool holds).

The pin safety valve (contract 2, additive, IMPROVEMENTS F3): when the
pinned account is refused (a 429 in the transcripts), out of quota (a
current reading at 100% on either window) or needs a sign-in (its refresh
token has expired, or its last read failed, counted, with an error only a
sign-in cures), a live daemon lifts the pin and rotates as usual, saying so
in its log, a notification and the audit log:

```json
{"at": "…", "kind": "unpin", "profile": "work", "from": "w1", "reason": "pin on w1 lifted: it was refused"}
```

A dry-run daemon lifts nothing; it writes that row once with `"dry_run":
true`. `--hard` (`pin_hard: true`) keeps the pin even then, as every pin did
before; a plain `pin` replaces a hard one, and `unpin` clears both. While a
lift is due, `why --json`'s `decision` carries `"unpin": "pin on w1 lifted:
it was refused"` and is the decision rotation makes unpinned.

`claudeswitch account unpin [<id>] [--profile P] --json` → `{"unpinned": ["work"]}`.
With an id, every profile pinned to it; with `--profile`, that one; with
neither, every profile.

`claudeswitch priority <id>... --json` (also `account priority`) — the
rotation order, written as given; accounts left out follow in config order:

```json
{"priority": ["a3", "a1"], "order": ["a3", "a1", "a2"]}
```

`order` is the enabled accounts in the order rotation spends them.
Errors: `not_found`, `invalid_value` (an id twice).

## recovery

`claudeswitch recovery [--identify] --json`:

```json
{"items": [{"slot": "r1", "profile": "default", "kept_at": "…", "seat": "person-3@org-3",
            "account": "a3", "who": null, "access_expires_at": "…", "refresh_expires_at": null,
            "renewable": true, "error": null}]}
```

`account` is the configured account with that seat; `who` is filled by
`--identify` (one identity lookup each, through the shared call budget).

`claudeswitch recovery restore <slot> <account> [--force] --json` →
`{"slot": "r1", "account": "a3", "seat": "…", "renewable": true, "cleared": true}`.

`claudeswitch recovery clear <slot> --yes --json` → `{"slot": "r1", "cleared": true}`;
without `--yes`: `confirmation_required`.

## use

`claudeswitch use <id> [--profile P] [--dry-run] --json`:

```json
{"account": "a2", "profile": "default", "from": "a1", "dry_run": false,
 "org_id": "org-2", "verified": true, "five_hour": 12.5, "seven_day": 40}
```

`verified` is false when the usage read that confirms the swap was rate
limited (the swap itself succeeded). With `--dry-run`:
`{"account", "profile", "dry_run": true, "from", "expect_org"}`. Errors:
`outside_pool`, `not_vaulted`, `live`, `failed`.

## Adding an account (owner decision M4)

Browser route, the live session untouched:

1. `claudeswitch login <id> --direct --json --no-open [--profile P] [--browser APP]`

   ```json
   {"url": "https://…", "expires_at": "…",
    "pending": {"account": "new-one", "profile": "work", "org_id": null, "new_account": true}}
   ```

   The app opens `url` itself (`--no-open`; without it `--browser APP`
   opens it in that browser). `--profile` is the pool a new account joins
   (D20). One login is pending at a time; starting another replaces it.

2. `claudeswitch login <id> --code <code> --json` — the id is required by
   the parser but the pending record decides which account completes:

   ```json
   {"account": "new-one", "seat": "person-4@org-4", "org_id": "org-4", "email": "…", "plan": "…",
    "profile": "work", "pool": ["w1", "new-one"], "configured": true,
    "new_account": true, "renewable": true, "access_expires_at": "…", "refresh_expires_at": "…"}
   ```

   Errors: `no_pending_login`, `wrong_account` (pinned seat mismatch; the
   message names both organizations), `already_vaulted`, `failed` (the
   code did not exchange: it must come from the most recent URL). On both
   steps, an id the config would not load is `invalid_value` and a missing
   or extra id `usage`.

`login` without `--direct` signs in through Claude Code interactively and
has no JSON form (`usage`).

Saving the login Claude Code is signed into now:
`claudeswitch add [<id>] [--from P] [--profile Q] [--force] --json` answers
with the same object as `login --code`, plus `"from"`: the profile whose
live credential was saved. `--from` (lane 16) is that source profile;
`--profile` is the pool a new account joins. Each defaults to the other,
and both to the profile of the caller's `CLAUDE_CONFIG_DIR`. With a source
other than the target (owner decision: allowed) the credential stays signed
in where it was: no profile swaps in an account live in another (§3), and
the source's daemon moves off it, since it is outside that pool. The answer
then carries `"note": "still signed in in S — S will move off it; T can use
it after"` (`null` otherwise), and the human output prints the same line;
the app shows it. An account another pool
lists is `outside_pool` for the target; an unknown `--from` is
`not_found`. With no id it refuses with `name_required`, the suggested
name (from the source profile's live account) as the whole `hint`; ask the
person, then run `add <name>` — and ask again when the source changes. Errors also:
`wrong_account`, `already_vaulted`, `outside_pool`, `invalid_value` (an id
the config would not load), `usage` (more than one id), `failed` (e.g. the
live credential is staler than the vaulted one: `--force`).

## daemon

`claudeswitch daemon status|start|stop|restart|live|dry-run|install|uninstall [--live|--dry-run] --json`
manages the service the way `install.sh` does — a launchd agent
(`~/Library/LaunchAgents/xyz.claudeswitch.daemon.plist`) or a systemd user
unit (`~/.config/systemd/user/claudeswitch.service`) — and answers every
verb with the status after it:

```json
{"action": "live", "platform": "launchd", "file": "/…/xyz.claudeswitch.daemon.plist",
 "log": "/…/daemon.log", "installed": true, "loaded": true, "running": true,
 "mode": "live", "binary": "/…/claudeswitch", "this_binary": "/…/claudeswitch",
 "binary_is_this": true, "daemon_live": true, "daemon_version": "0.5.0", "since": "…"}
```

- `install` writes the plist/unit for **the running binary** (it builds
  nothing; through a `cs` link, the `claudeswitch` it names) and loads it.
  A binary that would not survive a restart is refused with
  `binary_not_durable`: one macOS runs from an AppTranslocation copy, one
  under `$TMPDIR`, `/tmp`, `/var/tmp` or `/private/var/folders`, or a
  `go run` build. Install the binary first (`./install.sh` puts it in
  `~/.local/bin`). A path with a control character is `invalid_value`;
  `%` and `$` are escaped in the unit. launchctl and systemctl are run by
  absolute path (`/bin/launchctl`, `/usr/bin/systemctl` or
  `/bin/systemctl`). It then loads it
  (`launchctl unload`+`load`; `systemctl --user daemon-reload` +
  `enable --now`). Installed means started at login. The mode is
  `--live`/`--dry-run`, else the installed one, else dry-run.
- `live` / `dry-run` rewrite the file in that mode, keeping the binary it
  runs, and reload it.
- `start` / `stop` load and unload it (launchd) or start and stop the unit;
  `restart` reloads. `stop` lasts until the next login or `start`.
- `uninstall` unloads and removes the file; the state, logs and vault stay
  (`claudeswitch uninstall` removes everything).
- `mode` is the installed file's; `daemon_live`/`daemon_version`/`since`
  are what the running daemon recorded (present only while one runs).
  `binary_is_this` false means the service runs another build.

Errors: `not_installed` (start/stop/restart/live/dry-run), `service_failed`,
`unsupported_platform`, `binary_not_durable`, `invalid_value`, `usage`. `install.sh` keeps working and writes the
same files (a test holds them equal).

## why

`claudeswitch why --json` is the rotation decision as `cs why` explains it,
read from config and state alone. With one profile:
`{"decision", "accounts", "best", "best_why", "on_best", "active_room",
"best_room"}`; with several,
`{"profiles": [{"profile", "pool", "thresholds", "decision", "accounts",
"best", "best_why", "on_best", "active_room", "best_room", "current"?}]}`. (Before contract 2 both shapes also
carried `dir`, the working directory project rules were judged against.)

`best` (lane 16, owner decision) is the account the app's "Switch to best"
moves the profile to now: of the profile's pool, the account other than the
live one that automatic rotation would pick — readable, not refused, not
over its `reserve`, clear of the landing margin, ranked by room — whatever a
pin or the cooldown says (the person asked). It is never an account live in
another profile, guarded as maybe live there (a ghost), or needing a
sign-in. When no account clears the landing margin, `best` is still the
best one inside it, and `best_why` warns:
`"w2 is only 5 points below its 85% session trigger, short of the 10-point
landing margin"`. When there is none, `best` is `null` and `best_why` says
why (`"work has no other account"`, `"no other account in work has room
(…)"`, `"no other account in work can be switched to (w2: live in
default)"`; accounts left out for those reasons are listed after `not
offered:`). `best_why` is `null` when `best` is set with room to spare.

`on_best`, `active_room` and `best_room` (contract 2, additive, IMPROVEMENTS
M12, owner decision 2026-10-08) are the measure behind the app's "Already
on the best". A room is the points an account sits below the trigger of its
binding window (the one closest to its own trigger: session against
`switch_at`, week against `switch_at_weekly`), negative past it: the figure
`best` is ranked by, the live account's carrying its projection.
`active_room` is the live account's, `best_room` is `best`'s; either is
`null` when unknown (no reading, one whose window has since reset, an
unreadable counted model, or no such account), never `0`. `on_best` is
`true` when there is a `best` and it has no more room than the live
account; it is `false` when either room is `null`, when `best` is `null`,
and while the live account is refused. Raw utilization does not decide it:
the live session at 80% of an 85% trigger (5 points) against a best whose
week is at 85% of 98% (13 points) is `on_best: false`. The app reads
`on_best` and falls back to comparing the higher of session and week only
when the key is absent (an older binary).

**Weekly expiry and runway** (contract 2, additive, IMPROVEMENTS F1 and F2).
Every entry of `accounts` carries:

- `weekly_resets_at` (RFC 3339 UTC) and `weekly_unused` (points: 100 less
  the weekly utilization, a counted model's limit standing in as for every
  decision): the weekly window as `prefer = "expiring"` weighs it. Both
  `null` when unknown (no reading, no weekly figure or reset time, or a
  reading of a week that has since reset), never `0`, whatever `prefer` is.
- `trigger_at` (RFC 3339 UTC, or `null`): when the account reaches its
  trigger at this pace (below).

And each profile (the top level with one profile, each `profiles` entry with
several) carries:

- `pool_dry_at` (RFC 3339 UTC, or `null`): when the pool has no eligible
  account left at this pace;
- `pool_forecast`: `dry` (`pool_dry_at` is set; the time is now when the
  live account is already past its trigger with nowhere to go), `refills`
  (before the pool would run dry, a refusal clears or a binding window
  resets on an account rotation cannot use now or one already being spent,
  giving room back, so it does not run dry at this pace: `pool_refills_at`
  is that moment; a reset of an account before rotation reaches it does not
  count — it gives back only what that account used before), `idle` (the
  live account's last two readings are level: not being spent) or
  `unknown`;
- `pool_refills_at` (RFC 3339 UTC, or `null` unless `refills`).

The forecast: the pace is the live account's burn in points a minute, from
its last two readings, both within 15 minutes (or three `poll_active`,
whichever is longer) of each other and of now; a level pair carries the rate
of the last rise only while that rise is that recent. Without such readings,
or with an account in the pool that has no usable reading, the forecast is
`unknown` and every time `null`: never a guess. The pool is spent as
rotation would spend it: the live account to its trigger, then each account
`best` could offer that clears the landing margin, in rotation's order
(`prefer` included), each to its trigger at the same pace. Accounts not
spent that way (inside the margin, reserved, refused, over their trigger,
live elsewhere, needing a sign-in) have `trigger_at: null`, and so does any
whose time falls after `pool_refills_at`. Example, one profile, `work-1`
live at 40% and rising a point a minute, `work-team` at 5%:

```json
{"decision": {"kind": "stay", "reason": "active account at 42%, under the 85% trigger"},
 "accounts": [
   {"id": "work-1", "active": true, "trigger_at": "2026-10-09T14:45:00Z",
    "weekly_resets_at": "2026-10-14T09:00:00Z", "weekly_unused": 90, …},
   {"id": "work-team", "trigger_at": "2026-10-09T16:05:00Z",
    "weekly_resets_at": "2026-10-09T23:00:00Z", "weekly_unused": 90, …}],
 "pool_dry_at": "2026-10-09T16:05:00Z", "pool_forecast": "dry", "pool_refills_at": null, …}
```

`status --json` carries the same: `trigger_at` on each account (from the
runway of the profile whose pool holds it) and the three pool keys at the
top level with one profile, in each `profiles` entry with several. The
daemon notifies once when `pool_dry_at` falls within 2 hours, and again only
after the forecast has moved beyond that or to `refills`.

## chrome

**C2: which Chrome profile.** One resolution serves `chrome open`, `chrome
signin`, the notices and the JSON below. For an account in profile P: the
account's own mapping (`rule` `account`), else P's `chrome` (`profile`), else
Chrome's last-used profile (`last_used`); `none` when none of them is known
(no mapping, no `chrome`, Local State unreadable). claudeswitch reads
Chrome's `Local State` read-only for `profile.info_cache` (folder → name) and
`profile.last_used`, and nothing else of Chrome's. A resolution is:

```json
{"account": "w2", "profile": "work", "profile_dir": "Profile 1", "name": "Work", "rule": "profile"}
```

`profile_dir` and `name` are `null` when unknown.

`claudeswitch chrome list --json` — every account → Chrome profile mapping,
by account, and whether this platform can open Chrome:

```json
{"chrome_profiles": [
  {"account": "w2", "profile_dir": "claudeswitch-w2", "added": "2026-10-07T12:00:00Z", "live_in": ["work"],
   "existing": false, "name": null}],
 "supported": true,
 "live": [
  {"account": "w1", "profile": "work", "profile_dir": "Profile 1", "name": "Work", "rule": "profile",
   "last_from": "w2", "last_switch": "2026-10-08T11:00:00Z"}]}
```

`live_in` is the Claude Code profiles the account is live in, `[]` when none.
`added` is `null` for a mapping with no recorded time. `list` never launches
anything and never reads the keychain, so the app may poll it.

C2 (additive): `existing` is `true` for a Chrome profile the person already
had (`add --existing`), `name` Chrome's name for the folder. `live` has, for
each profile with a live account, that account's resolution plus the last
rotation in the profile: `last_from` (the account it moved away from, state's
`last_from`, recorded by the daemon and `cs use` since C2) and `last_switch`
(UTC), each `null` when unknown. The app's card shows its sign-in notice when
`rule` is `profile` and `last_from` is set and differs from `account`.

`claudeswitch chrome profiles --json` (C2) — Chrome's profiles from Local
State, in Chrome's order (Default, Profile 1, …), then any folder a profile
or an account names that Chrome does not list (`in_chrome` false):

```json
{"chrome_profiles": [
  {"folder": "Default", "name": "Person 1", "last_used": false, "in_chrome": true,
   "used_by_profiles": ["default"], "used_by_accounts": []},
  {"folder": "Profile 2", "name": "Work 2", "last_used": true, "in_chrome": true,
   "used_by_profiles": [], "used_by_accounts": ["w3"]}],
 "last_used": "Profile 2", "local_state": true, "supported": true}
```

`local_state` is `false` (and the list only what claudeswitch names) when
Local State is missing or unreadable; `last_used` is then `null`. It never
launches anything.

`claudeswitch chrome add <account> --json` — opens Chrome on a profile
directory derived from the account (`claudeswitch-<account>`) at the claude.ai
login and Claude in Chrome's Web Store page, and records the mapping:

```json
{"account": "w2", "profile_dir": "claudeswitch-w2", "created": true, "opened": true,
 "email": "w2@example.com",
 "urls": ["https://claude.ai/login", "https://chromewebstore.google.com/detail/fcoeoabgfenejglbffodgkkbkcdhcgfn"]}
```

`created` is `false` when the account already had a profile `add` made: it
is opened again and the mapping is unchanged. An account mapped with
`--existing` gets a new one (`created` `true`).

`claudeswitch chrome add <account> --existing <folder|name> --json` (C2) —
maps the account to a Chrome profile the person already has. It creates
nothing and opens nothing:

```json
{"account": "w2", "profile_dir": "Profile 2", "name": "Work 2", "created": false, "existing": true,
 "opened": false, "email": "w2@example.com", "urls": []}
```

A name resolves through Local State (the `hint` of a `not_found` lists
Chrome's profiles); without Local State the value is taken as a folder.
`--existing` with any other subcommand is `usage`. `email` is the one state.json recorded when
the account was vaulted or identified (no keychain read) and is `null` when
none was recorded. The mapping is
recorded only once Chrome has been opened.

`claudeswitch chrome [open] [<account>] --json` — opens the account's
profile; with no account, the profile of the account live in the caller's
Claude Code profile (`CLAUDE_CONFIG_DIR` → profile → live account):

```json
{"account": "w2", "profile_dir": "claudeswitch-w2", "opened": true}
```

C2: it opens the resolved Chrome profile, so an account with no mapping
opens its profile's or Chrome's last used; the answer carries the
resolution's keys too (`profile`, `name`, `rule`). With nothing resolved:
`not_found`, the `hint` naming `cs profile set <p> chrome <name>` and
`cs chrome add <account>`. It never creates a Chrome profile.

`claudeswitch chrome signin [<account>] --json` (C2) — opens the resolved
Chrome profile at the claude.ai login and Claude in Chrome's Web Store page,
to sign both in as the account (an argv, never a shell; no keychain read):

```json
{"account": "w2", "profile": "work", "profile_dir": "Profile 1", "name": "Work", "rule": "profile",
 "opened": true, "email": "w2@example.com",
 "urls": ["https://claude.ai/login", "https://chromewebstore.google.com/detail/fcoeoabgfenejglbffodgkkbkcdhcgfn"]}
```

`email` is state's recorded email, `null` when none. Errors as for `open`,
and `not_found` for an unknown account.

`claudeswitch chrome forget <account> --json` — drops the mapping. The Chrome
profile itself is not touched:

```json
{"account": "w2", "profile_dir": "claudeswitch-w2", "forgotten": true}
```

Errors: `not_found` (no such account; nothing to open for it, with a `hint` naming
`cs chrome add <account>`; no live account in the caller's profile; no Chrome
binary on Linux), `invalid_value` (the derived directory is not a valid name,
e.g. an id over 51 characters, or a recorded one no longer is), `unsupported_platform` (neither macOS nor
Linux), `config_invalid`, `usage`, `failed` (the launcher failed; `hint` is
its output).

`claudeswitch chrome hint` is the plugin's PostToolUseFailure
hook, not an app action: it reads the hook's JSON on stdin.

## history

`claudeswitch history --usage [--days N] --json` (F7, additive; default 30
days) — each account's readings over the span and the switches, for
Settings → History. It reads the daemon's readings log
(`~/.local/state/claudeswitch/readings.jsonl`), the audit log and the
config; never the keychain or the API:

```json
{"days": 30, "from": "2026-09-09T12:00:00Z", "to": "2026-10-09T12:00:00Z",
 "accounts": [
  {"id": "w1", "profile": "work", "configured": true,
   "series": [{"at": "2026-10-09T11:58:02Z", "five_hour": 42, "seven_day": 61.5}]},
  {"id": "w2", "profile": "work", "configured": true, "series": []}],
 "switches": [
  {"at": "2026-10-09T10:01:00Z", "profile": "work", "from": "w1", "to": "w2",
   "reason": "active account at 86%, over the 85% session trigger", "forced": false}]}
```

- `accounts`: the configured accounts in `account list`'s order, then any
  the log names that the config does not (`configured` false, `profile`
  null). `profile` is the pool owner.
- `series`: oldest first; `at` UTC; `five_hour` / `seven_day` percentages,
  `null` when that window was not reported (never 0). The daemon writes one
  point per new reading; points older than a day are thinned to one per
  account per 15 minutes, and nothing older than 30 days is kept.
- `switches`: the audit log's `switch` events in the span, oldest first
  (the daemon's, `use`'s, a profile seed's); `from` null when unknown.

Errors: `usage` (`--json` without `--usage`; `--days` under 1), `failed`
(a log that cannot be read). A config that does not load is not an error:
the accounts are then the log's, `profile` null.

## doctor

`claudeswitch doctor [--verify] --json` (F12, additive) — every check
doctor prints, for Settings → Health:

```json
{"checks": [
  {"name": "daemon", "status": "fail", "level": "fail", "message": "older than this binary (0.5.6)",
   "details": ["the running daemon (0.5.5, started …) is older than this cs (0.5.6); restart it: …"],
   "fix": "daemon restart", "account": null, "profile": null},
  {"name": "refresh token", "status": "warn", "level": "warn",
   "message": "personal           access in 5h0m0s · refresh token expires in 3d",
   "details": [], "fix": "signin personal", "account": "personal", "profile": null}],
 "failed": 1}
```

- One object per row of the text (`[ok  ]`, `[warn]`, `[FAIL]`, `[info]`),
  in its order: `name` the row's name, `message` the rest of the row,
  `details` its `└` lines. `status` is `ok`, `warn` or `fail`; `level` is
  the row's mark (`info`, a note, has `status` `warn`).
- `fix` is `signin <account>`, `daemon restart`, `statusline install`,
  `keychain allow` or `null`:
  - `daemon restart`: the running daemon is older than this binary;
  - `statusline install`: the status line is not set;
  - `keychain allow`: macOS could not read the live credential (approve the
    prompt, Always Allow);
  - `signin <account>`: on the per-account checks below.
- Per-account checks (`account` set; in the text they are detail lines):
  `refresh token` for each vaulted account (`warn` with `signin <id>` when
  its refresh token is missing, expired or within 5 days of expiring), and
  with `--verify`, `credential` for each configured account (`ok`; `fail`
  when it does not sign in or holds another seat; `warn` when not vaulted;
  `signin <id>` unless `ok`). A `profile` check carries `profile`.
- `failed` is the number of `[FAIL]` rows. The exit status is 0 whenever the
  report was made, failures included; the text form keeps exiting 1 on a
  `[FAIL]`. `doctor` reads the live credential and, for the refresh rows,
  each vaulted credential, so on macOS it may raise keychain prompts: run it
  when the person asks (the Health pane), not on every refresh.

## init

`claudeswitch init --empty --json [--config PATH]` (0.6.1, additive) — an
empty config for the first-run window, which needs one before `add --json`
can append to it. The app never writes claudeswitch's files itself.

```json
{"path": "/…/.config/claudeswitch/config.toml"}
```

- Writes comment lines only (no accounts, no settings: every setting keeps
  its default) at `--config`, or the default path, with mode 0600 and its
  folder 0700.
- Errors: `exists` (a file is already there; nothing written), `usage`.
- A binary before 0.6.1 answers `flag provided but not defined: -empty`
  (exit 2); the app reads that as too old and says to update or run
  `cs setup`.
- Without `--empty`, `init --json` writes the commented starter template
  and answers the same object.

## app

`claudeswitch app heartbeat --json` (0.6.1, additive) — the app is running
and posts its own actionable rotation notifications. The app runs it every
minute while it does; the daemon skips its own rotation notice while the
time it records is in the future.

```json
{"app_notifies_until": "2026-10-08T12:02:00Z"}
```

- Records `app_notifies_until` = now + 2 minutes in state.json (under the
  state lock, changing nothing else). The app never writes state.json.
- The daemon reads it from the file at each rotation. Only its rotation
  notice is skipped; every other notice is sent as before. With no app (or
  on Linux), or two minutes after the last heartbeat, it notifies as
  before.
- Errors: `usage` (anything but `heartbeat`), `failed`. A binary before
  0.6.1 has no `app` command (usage text, exit 2): the app reads that as
  too old, stops nothing, and the daemon keeps notifying.
