# Multiple Claude Code profiles — design (2026-10-07)

Status: **implemented.** Design decided 2026-10-07 (§8); all five build steps
(§9) are in, including the CLI (§6): `--profile`, the env-resolved default,
the per-profile status line and views, and D14. The README's "Multiple
Claude Code profiles" section is the user-facing description.

Naming: this was designed and built as "instances" (`[[instance]]`,
`--instance`, `docs/MULTI_INSTANCE.md`); D19 renamed it to "profile" before
release. Earlier commits, review notes and code history say "instance"; the
decision IDs D1–D25 are unchanged.

## 1. The problem

Claude Code can run as several independent profiles on one machine, one per
config directory:

```
~/.claude          # CLAUDE_CONFIG_DIR unset
~/.claude-work     # CLAUDE_CONFIG_DIR=~/.claude-work claude
```

Each profile has its own credential, identity file, settings and transcripts.
claudeswitch today drives exactly one, and does not even agree with itself
about which:

| Piece | Today | Where |
|---|---|---|
| settings.json, plugin check | `$CLAUDE_CONFIG_DIR` or `~/.claude` | `cmd/claudeswitch/claudecode.go:117,349` |
| live credential (swap, refresh, attribution) | always `Claude Code-credentials` | `internal/credstore/credstore.go:88` |
| transcripts (activity, rejections) | always `~/.claude/projects` | `internal/detector/detector.go:109` |
| Linux credential file | always `~/.claude/.credentials.json` | `internal/credstore/store_linux.go:27` |

So a second profile is invisible to rotation, and running claudeswitch with
`CLAUDE_CONFIG_DIR` set splits it across two profiles.

## 2. Ground truth: where a profile keeps its credential

Read from Claude Code 2.1.293 (the keychain service-name function):

```js
suffix = CLAUDE_CONFIG_DIR unset ? "" : "-" + sha256(configDir.normalize("NFC")).hex.slice(0, 8)
service = "Claude Code" + OAUTH_FILE_SUFFIX + "-credentials" + suffix
```

`CLAUDE_SECURESTORAGE_CONFIG_DIR`, if set, overrides which dir is hashed (and
set-but-empty means "no suffix").

Consequences:

- **The hash is of the string as Claude Code sees it.** `~/.claude-work`,
  `/Users/x/.claude-work` and `/Users/x/.claude-work/` are three different
  items. claudeswitch must not compute the name and trust it. It computes the
  candidates and confirms which one exists by a metadata-only lookup
  (`security find-generic-password -s <svc>` without `-w`), which does not
  prompt.
- **Setting `CLAUDE_CONFIG_DIR=~/.claude` explicitly is a different profile
  from leaving it unset**, as far as the keychain is concerned.
- Identity (`oauthAccount`) lives in `$dir/.claude.json`, not `~/.claude.json`.
  Transcripts live in `$dir/projects`.

## 3. The invariant that shapes everything

**One credential may be live in at most one profile at a time.**

Claude Code refreshes its own token, and a refresh revokes the previous refresh
token. If one account's credential sits in two profiles' keychain items, the
first profile to refresh kills the other's copy. That is the
one-credential-two-names failure from 2026-09-11, rebuilt across profiles.

Fixed, disjoint pools (§8 D1) enforce this structurally: config validation
rejects an account named in two pools, so rotation can never pick across
profiles. A runtime check stays anyway, before every write to a live item,
because manual commands and config reloads are where that kind of structural
guarantee breaks.

## 4. Model

```
Profile {
  name      string   // "default", "work" — config key, shown in status
  dir       string   // as the user launches Claude Code with it
  service   string   // resolved keychain item (or Linux file), §2
  projects  string   // dir/projects
  identity  string   // dir/.claude.json
  pool      []string // account ids; disjoint across profiles (§8 D1)
  overrides          // optional switch_at / switch_at_weekly / hard_floor (§8 D4)
}
```

Config:

```toml
[[profile]]
name = "default"        # no dir: Claude Code runs with CLAUDE_CONFIG_DIR unset
pool = ["personal", "a4", "a5"]

[[profile]]
name      = "work"
dir       = "~/.claude-work"
pool      = ["work-1", "work-2"]
switch_at = 75          # overrides the global 85 for this profile only
```

**`dir` is `CLAUDE_CONFIG_DIR` exactly as the person launches Claude Code with
it, and omitting it means `CLAUDE_CONFIG_DIR` unset** (§2). The two are
different profiles even when they name the same directory:

| config | keychain item | identity file | files |
|---|---|---|---|
| no `dir` | `Claude Code-credentials` (bare, no lookup) | `~/.claude.json` | `~/.claude` |
| `dir = "~/.claude"` | `Claude Code-credentials-<hash>`, confirmed by lookup | `~/.claude/.claude.json` | `~/.claude` |

So the profile most people already have is written with no `dir`. Validation
(an invalid config is an error, never a crash):

- names are unique and non-empty;
- at most one profile omits `dir`: two would read the same bare keychain item,
  one credential live in two profiles (§3);
- pools are disjoint (D1; the error names both profiles), name only
  configured accounts, and list no account twice;
- accounts in no pool join `default` (D6); with no `default` declared, an
  unlisted *enabled* account is an error;
- `switch_at`, `switch_at_weekly`, `hard_floor` overrides are in (0, 100], and
  a profile's effective `hard_floor` is at or above its effective `switch_at`.

Two profiles on one folder, or one inside another's, are a **config error**
(D27, superseding D12's warning): `profiles "default" and "alt" are the same
folder (~/.claude → /Users/x/.claude)`. Folders are compared as real paths:
`~` expanded, absolute, symlinks resolved as far as the path exists, an
omitted `dir` counting as `~/.claude`, and case-folded on macOS. Their
keychain items differ, but their transcripts are one directory (and on Linux
their credential file is one file), and a link makes one profile's files the
other's.
A profile with an empty pool is allowed too; `doctor` warns about it without
failing.

No `[[profile]]` blocks: one implicit `default` holding every account, which
resolves its dir from the process environment exactly as before (§7). A
configured profile resolves from its `dir` alone, never from the daemon's
environment; `CLAUDE_SECURESTORAGE_CONFIG_DIR` is not modelled for it.

Code: `config.Profile`, `Config.EffectiveProfiles`, `Config.ForProfile`
(effective thresholds); `profile.Resolve` (dir, projects, identity, Linux
credential file, keychain item via `credstore.LiveServiceFor`); `doctor` prints
one block per profile. `state.Load(path, names...)` materialises every
configured profile, so `Profile(name)` on those names never writes the map.

State changes from one active account to one per profile:

```go
// before
Active   string    `json:"active_account"`
ActiveAt time.Time `json:"active_at"`

// after
Profiles map[string]*ProfileState `json:"profiles"`  // keyed by profile name
type ProfileState struct {
    Active, ActiveAt, LastSwitch, Pinned ...
}
```

Migration: an old `active_account` loads as `profiles["default"]`. The
ownership rule (daemon owns Active, `ActiveAt` breaks ties) applies per
profile, unchanged.

Account readings (`Accounts[*]`) stay global. An account's quota is the same
whichever profile spends it.

## 5. Daemon

One daemon, as now (the daemon lock and the machine-wide call budget stay).
Inside it:

- **one detector per profile**, each watching its own `projects` dir. A
  rejection is attributed to *that profile's* active account. Today it goes to
  the single `st.Active`, which would be wrong for a second profile.
- **one `evaluate` per profile**, each with its own idle gap, `wantSwitchSince`,
  cooldown, pin and thresholds. Candidates are that profile's pool only.
  `policy.Input` gets the pool and the effective thresholds, so `Decide`
  itself does not need to know about profiles.
- **one poller, shared.** Each account is polled once regardless of how many
  profiles could use it. "Active" cadence applies to every account that is
  active somewhere.
- `SwapTo(profile, account)` writes that profile's service. Rollback and
  verify are unchanged.
- Re-attribution (`PollActive`) runs per profile, against that profile's item
  and identity file.

### Budget

At the current config (`api_budget = 12`, `poll_active = 3m`, `poll_idle = 10m`,
five accounts), two active accounts cost about 4.8 calls per five minutes
against an allowance of 11, so it fits. Each account also has its own
allowance (DESIGN 4.3c), which is per account and so the same however many
profiles there are. Hot polling is movement-driven (DESIGN 4.3c) and at
`poll_hot = 60s` one hot account is 5 calls per five minutes on top of the
rest. Two hot profiles would still compete for the shared window, so only a
*busy* profile polls hot (§8 D3): hot needs movement within reach of the trigger **and**
transcript activity within the idle window. A hot but quiet profile polls at
`poll_active`. It is not spending, so a stale reading costs nothing.

## 6. CLI and status line

- Every command that touches the live credential (`use`, `refresh`, `whoami`,
  `login`, `status`) takes `--profile NAME`. With no flag it resolves the
  profile from the caller's own `CLAUDE_CONFIG_DIR`, so `/cs switch` inside a
  work session switches work, with no flag needed.
- The status line runs inside a session and knows its profile the same way.
- `status` and `top` show one block per profile.
- `doctor` checks each profile: dir exists, keychain item resolved, identity
  matches the active account.

As built (lane 5):

- `use`, `add`, `login` (without `--direct`) and `whoami` act on one profile:
  `--profile NAME`, or else the caller's `CLAUDE_CONFIG_DIR` — unset or empty
  is the profile that omits `dir` (D11); set, it matches a `dir` as written
  first, then as the directory it names (`~` expanded, trailing `/` ignored).
  No match, or two equal matches, is an error naming the profiles and the
  flag. No `[[profile]]` blocks: the implicit profile, as before.
- `use`, `login` and `add` (for a configured id) refuse an account outside the
  target's pool (D5) before touching the vault; `use` and `login` also refuse
  one that is or may be live in another profile, with the daemon's own §3
  check (`liveElsewhereOf`, shared), unknown counting as live (D18).
- `refresh` and `remove` ask every profile's live item, not one: a refresh
  revokes the token wherever it is live, so no flag narrows that check.
- `status`, `top`, `why` and `plan` print one block per profile (pool,
  active account, decision, effective thresholds) and take `--profile` to
  show one. Their JSON keeps the single-profile shape and adds a `profiles`
  list, marking the caller's profile `"current": true`. The status line and
  the session-start context show their own session's profile.
- With no `[[profile]]` blocks, the output of status, why, plan and the status
  line is pinned to what it was before (`cmd/claudeswitch/testdata/pin`).

## 7. Compatibility

No profile config means a single implicit profile named `default`, which
resolves from `CLAUDE_CONFIG_DIR` exactly as Claude Code does. Every path in §1
now resolves consistently. That fix ships first, on its own, and is useful
without the rest.

## 8. Decisions (2026-10-07)

- **D1. Pool model: fixed pool per profile.** Each profile rotates only within
  its own `pool`. Pools must not overlap (config error). Chosen over a shared
  pool with exclusion: predictable, and keeps work and personal separate, at
  the cost that headroom in one pool cannot help the other.
- **D2. Declaration: explicit `[[profile]]` blocks.** Nothing unlisted is
  touched. No blocks means one implicit `default` profile holding every
  account, which is today's behaviour.
- **D3. Hot polling: busiest profile only.** See §5 Budget.
- **D4. Thresholds: per-profile override.** An `[[profile]]` may set
  `switch_at`, `switch_at_weekly`, `hard_floor`; anything unset falls back to the
  global value. `status` shows the effective value per profile.

- **D5. Manual `use` outside the pool is refused**, naming the profile that
  owns the account. There is no `--force`: a manual swap across pools is how the
  two-profiles-one-credential failure (§3) comes back.
- **D6. Accounts in no pool join the profile named `default`.** If profiles
  are declared and none is named `default`, an unlisted enabled account is a
  config error rather than a guess about which profile it belongs to.
- **D7. NFC: normalise like Claude Code.** The hashed dir goes through
  `norm.NFC` (`golang.org/x/text`) before sha256, so a decomposed accented path
  resolves to the same item Claude Code uses. Accepted cost: one new dependency.
- **D8. A literal `~` is expanded for file paths.** The daemon's working
  directory is not the person's, so reading `~` literally would look in the
  wrong place. The keychain name is still hashed from the string as given.
- **D9. No fallback to the plain item.** With `CLAUDE_CONFIG_DIR` set and no
  suffixed item, claudeswitch refuses to read or write. If the plain item
  exists, the error says the profile is probably not logged in yet, and how to
  log it in.
- **D10. The old top-level state fields go at the multi-profile release.**
  Until then `state.json` writes both `profiles` and the old
  `active_account`/`pinned`/`last_switch`/`active_at`, so an older daemon keeps
  working mid-upgrade. The release that adds `[[profile]]` config writes
  `profiles` only, with a release note to restart the daemon after upgrading.
- **D11. Leaving out `dir` means `CLAUDE_CONFIG_DIR` unset** (plain keychain
  item, `~/.claude.json`, `~/.claude`). At most one profile may leave it out:
  two would share the plain item, which is the §3 failure.
- **D12 (superseded by D27). Two profiles sharing a directory load with a warning**, not an
  error: activity and refusals may be attributed to the wrong one.
- **D13. An empty pool is allowed**; `doctor` warns that the profile will
  never be rotated.
- **D14. `doctor` exits non-zero on any FAIL**, not only the profile checks.
  Warnings never fail it. Lands in lane 5.
- **D15. "Busy" for D3 means transcript writes within `poll_active`.** An
  profile between turns still counts as busy; the 8s idle gap would not. (3 minutes at the
  default since lane 11.)
- **D16. The D3 gate applies only with two or more profiles.** With none or
  one, the busy gate does not apply (movement within reach, DESIGN 4.3c, always does).
- **D17. A live credential from another profile's pool is recorded as it is**,
  with an error logged; that profile's next decision swaps it back into its
  own pool, provided the target is not live elsewhere.
- **D18. When the daemon cannot tell whether another profile holds the
  target, it refuses the swap and retries.** An unreadable item, a failed
  identity lookup, or a lookup error all count as unknown. The profile does not
  rotate until the doubt clears; each refusal is logged, audited and notified
  once. The probe's verdict is cached so a lasting refusal cannot drain the call
  budget.

- **D19. The user-facing name is "profile", not "instance"** (decided
  2026-10-07, before release). "Workspace" clashes with editor/project
  workspaces and Anthropic Console workspaces; "namespace" is too technical;
  "profile" is the familiar word for a separately configured setup you choose
  at launch (browser, AWS CLI). The rename covers `[[profile]]`, `--profile`,
  `profiles` in state/JSON, the status line, the menu-bar app and the docs.
  Landed in lane 8, internal names included (package `internal/profile`,
  `config.Profile`, `state.ProfileState`). No release read the old names, so
  there is no alias, with three exceptions for files dev builds wrote:
  `[[instance]]` in the config is refused with a rename hint (not ignored,
  which would silently merge every pool); `state.json` still reads
  `instances` (and a ghost's `instance`) when `profiles` is absent and writes
  `profiles` only; old audit rows' `instance` reads as `profile`. JSON:
  `status`/`why`/`plan --json` say `profiles` and `profile`; per account,
  `profile` is the pool owner and `active_in` (unchanged) is the profile it
  is live in.

- **D20. A new account added by `login --profile P` joins P's pool**: the
  config edit appends it to that profile's `pool` as well as writing the
  account block, and says so.
- **D21. Profile changes hot-reload too.** Adding, removing or re-pointing a
  `[[profile]]` while the daemon runs starts or stops that profile's loop and
  transcript watcher in place (re-resolving its live item for a new `dir`),
  never mid-swap; no restart is ever needed for a config edit. Chosen over a
  self-restart (2026-10-07).

- **D22. Removed or re-pointed profiles become ghosts** for the §3 check: their
  old live item and last active account keep being consulted by every
  one-credential check (daemon and CLI), persisted across restarts, until the
  old item no longer holds that account or the user runs `forget`. Hot reload
  must never be a way around §3.
- **D23. Account and profile names are strict** (letters, digits, `.`, `_`,
  `-`; no leading `-`/`.`; no `..`; ≤ 64). A config with an older non-conforming
  id refuses to load, naming the id and the `cs rename` command, and `rename`
  works on such a config, moving the vault item too. Suggested names are always
  valid.

- **D24. Pools stay separate (2026-10-07).** Overlapping pools were
  considered — an account listed in several profiles, live in one at a time —
  and withdrawn the same day: D1 stands, and an account belongs to exactly one
  profile.

- **D25. Offline config edits make ghosts too.** A profile recorded in
  `state.json` with an active account but missing from the config (or
  re-pointed) at daemon start or in a CLI check becomes a ghost, using the item
  reference state recorded, or guarding the account by name if none was
  recorded. Stop-edit-start is common, and `rename` requires a stopped daemon.

As built (lane 7):

- D20: `login <id>` and `add <id>` for an id the config does not have, with
  `[[profile]]` blocks declared, append it to the target profile's `pool`
  in the same textual edit that writes the account block (one write, parsed
  back, the id checked to land in that pool; mode and symlink kept). Pools on
  one line, across lines, empty or absent are edited in place. With no
  `default` profile this is what makes the new account loadable at all.
  `login --direct --profile P` (owner decision, lane 7) records P in the
  pending-login record, and `login --code` appends the new id to P's pool
  the same way, provided P is still declared. With profiles declared, no
  `--profile` and no `default` profile, a new `--direct` id is refused up
  front asking for `--profile`; with a `default`, it joins default (D6).
- D21: `daemon.reload` diffs the profile set. Removed and re-pointed
  profiles are stopped first; the new config is adopted; added and
  re-pointed ones are built, resolved, handed to the poller and launched;
  loops are kept in config order. Stopping marks the loop stopped, closes its
  own quit channel, waits for its forwarder, waits up to 5 s for its
  detector, and drops its live item from the poller; its hold and wait timers
  go with the loop. Everything runs on the run goroutine, where swaps run
  synchronously, so a reload is never applied mid-swap. Logged as INFO
  `profile started` / `profile stopped`.
- A removed profile's `ProfileState` stays in `state.json` (state never
  drops a profile; a save merges the disk's back). A profile started by
  a reload with an active account already recorded — re-pointed, or removed
  and declared again — has it cleared, since it described another or a past
  item, and holds (as after a reload that drops the active account) until
  its item is attributed.
- Ghosts (lane 7 security review, owner decision): a removed or re-pointed
  profile whose state recorded an account live in it leaves its old item as
  a `state.Ghost` (`ghosts` in `state.json`: profile, why, the resolved
  keychain service and Linux credential file, account, seat, since). Every
  §3 check consults ghosts like profiles — the daemon's `liveElsewhere` and
  `liveAnywhere`, and the CLI's `use`, `login`, `refresh` and `remove` — so
  the account is installed nowhere else while it may still be live there; a
  refusal says "may still be live in removed profile B". A ghost whose item
  is in use by a current profile is skipped (that profile covers it). The
  daemon releases a ghost (INFO) when `HoldsAccount` says its item is known
  not to hold the account, checked at the re-attribution cadence; unknown
  keeps guarding (D18). `cs profile forget <name>` releases one by hand.
  Ghosts survive a daemon restart; saves merge them so a CLI forget and a
  daemon's new ghost are both kept. `status` and `doctor` list them.
- Edits made while no daemon runs (owner decision, lane 7): the daemon
  records each profile's resolved item in its state (`item_ref`: service,
  credential file, dir, FromEnv) whenever it resolves it. A profile state
  records with a real active account that the config no longer declares, or
  declares on another dir than its recorded item came from, implies a ghost:
  the daemon adopts it at startup (WARN naming the profile; the record's
  active account moves to the ghost, a re-pointed profile holds), and the
  CLI's checks (`use`, `login`, `refresh`, `remove`) honour it with no daemon
  running. With no item ever recorded, the ghost guards the account by name:
  it is refused everywhere until `cs profile forget <name>`, which is the
  only release for such a ghost. A removed profile's record keeps no active
  account once its ghost exists, so a release or forget sticks.
- Final review (lane 7): a record named `default` with no item — the
  implicit profile, or one migrated from a pre-profile state file — guards
  the item this environment names, not the account by name: it is filtered
  against the profile using that item now (a `[[profile]]` with no `dir`,
  whatever its name) and released like any item ghost. A by-name ghost is
  released when its profile is declared again. A CLI save takes each
  profile's item from disk (the daemon owns it). Release note: restart the
  daemon once after upgrading, before adding `[[profile]]` blocks, so items
  are recorded.
  `use` and `login` run the check whenever there are other profiles or any
  ghost, so consolidating to one profile does not switch it off. A profile
  re-pointed again before its new item was attributed leaves a ghost carrying
  the account its hold remembers; a profile started on a ghost's item (one
  declared again on its old dir) releases that ghost.

As built (lane 10, IMPROVEMENTS I7):

- `cs profile create <name> [--dir PATH] [--pool a,b] [--seed <account>]`.
  Every check runs before anything is made, so a refusal leaves no dir and
  no config edit. The dir defaults to `~/.claude-<name>` and is written to
  the config absolute, the string `cs run` then sets, so the keychain item
  Claude Code hashes from it (§2) is the one seeded. Refused: an existing
  profile name; a dir that is, holds or sits inside `~/.claude`, another
  profile's dir or this shell's `CLAUDE_CONFIG_DIR`, compared as real paths
  (D27; inside `~/.claude` the links would loop); `--dir ~user/…`; an
  unconfigured or repeated account; and an account another profile's pool
  *lists* (D1, naming the owner). An account that joins `default` only by
  D6 may be taken. With no `[[profile]]` blocks, a `default` block is
  declared in the same edit, or the accounts left out would join nothing:
  with this shell's `CLAUDE_CONFIG_DIR` set, its `dir` is that value as
  written, so it stays the profile the implicit one was; unset, no `dir`
  (D28). The edit is textual and parsed back (`writeConfigFile`: mode and
  symlink kept). An existing dir not 0700, or any dir in a git work tree,
  gets a warning: the MCP copy can carry secrets. Printed commands quote
  the dir.
- Shared by symlink from `~/.claude`, when present and not already in the
  dir: `settings.json`, `CLAUDE.md`, `skills/`, `commands/`, `agents/`.
  Never: `projects/`, history, plugins, credentials. User-scope
  `mcpServers` from `~/.claude.json` are copied into a new
  `<dir>/.claude.json` holding that key alone (0600, never over an existing
  file); OAuth MCP logins live in the base profile's credential and are not
  copied.
- `--seed`: the account must be in the new pool, vaulted and renewable; the
  new profile must have no credential under any spelling of its dir (a
  failed lookup refuses); and the account must not be live or maybe-live
  elsewhere — `liveElsewhereOf` over every profile and ghost, unknown
  counting as live (D18) — checked before anything is made and again just
  before the write, against the config and state read afresh. With a daemon
  running the seed first waits (up to 10 s) for it to load the new profile
  (D29): the daemon writes `daemon_profiles` (name → dir) to state at start
  and on every reload, and until the profile is there the daemon's own §3
  checks do not know it. Timing out refuses the seed — the profile itself is
  made — and says to retry with `cs profile seed <name> <account>`, which
  makes every create-time seed check on an existing profile with a dir.
  With no daemon it seeds at once. The write creates the
  item Claude Code names for the dir (`LiveServiceName`): on macOS
  `add-generic-password` without `-U` (create-only, after a metadata lookup
  says it is absent) and without `-T` (§41), within the 4032-byte line
  (§43); on Linux `<dir>/.credentials.json`, 0600, linked into place so an
  existing file is never replaced. It holds Claude Code's two credential
  locks for the dir (cclock). Afterwards the item is resolved as every later
  command resolves it (`LiveServiceFor`) and must be the one written. The
  blob is the vaulted `claudeAiOauth` alone. State records the account
  active in the new profile, so other profiles' checks see it at once.
- `cs run <profile> [-- args]` execs `claude` with `envForProfile`'s
  environment (`CLAUDE_CONFIG_DIR` the dir as written, unset for the no-dir
  profile, `CLAUDE_SECURESTORAGE_CONFIG_DIR` removed). A profile whose live
  credential does not resolve (on Linux, whose file is missing) is started
  anyway, with one stderr note (D30): `profile "work" is not signed in yet —
  use /login, then claudeswitch takes over`. No daemon interaction: D21
  reloads the config.
- `cs profile list`: name, dir, pool, the account state records live, and
  whether the credential resolves.

Owner decisions from the lane 10 security review:

- **D27. Profiles on one folder are a config error**, at load and in
  `profile create`: the same real path (links resolved, case-folded on
  macOS) or one inside another's, the no-dir profile counting as
  `~/.claude`. It supersedes D12: every case D12 warned about is one of
  these, so the warning is gone.
- **D28. The `default` block `profile create` adds** to a config without
  profiles takes this shell's `CLAUDE_CONFIG_DIR` as its `dir` when set, and
  has no `dir` when unset.
- **D29. A seed waits for a running daemon** to load the new profile, then
  checks §3 afresh; on timeout it refuses, leaving the profile made.
- **D30. `cs run` starts a profile that is not signed in**, with a note,
  rather than refusing: /login inside it is how it is signed in.

Re-review (lane 10): a starting daemon replaces `daemon_profiles` with its
own set and saves it first thing, before any network call, so a seed
waiting through a daemon restart never reads the previous daemon's entry as
"loaded". A relative profile `dir` (`cc`, `./cc`, `../cc`) is a config
error naming the profile: the CLI and the launchd daemon have different
working directories, so it would name two folders; write it absolute or
from `~/`.

As built (lane 15):

- `cs profile remove <name> [--to <profile>]` removes a `[[profile]]` block
  (docs/APP_CLI.md). Its pool moves to `--to`, else to `default` (D6);
  with neither, an enabled account would be in no pool and the command
  refuses (`would_orphan`). The last profile cannot be removed, nor
  `default` without `--to` while others exist. The profile's folder and
  keychain item are never deleted. The account last live there becomes a
  ghost (D22): a running daemon's reload makes it (D21) and the command
  waits for it to load the edit; with no daemon the command records it
  itself under the daemon lock, as `adoptOfflineGhosts` would at the next
  start (D25).

- **D26. The profiles release is 0.5.0**, and it publishes the menu-bar app as
  an unsigned zip from the public repo's release workflow (macOS runner,
  `macos/scripts/app-zip.sh`), alongside the build-from-source route.

## 9. Build order

1. Single-profile consistency (§7): detector root, Linux path, live service
   from `CLAUDE_CONFIG_DIR`, with metadata-lookup resolution. Ships as a patch.
2. State migration to per-profile active, still one profile.
3. Profile config + resolution + `doctor` checks.
4. Daemon: per-profile detector and evaluate, exclusion invariant, shared poller.
5. CLI `--profile`, env-resolved default, status line, `status`/`top` blocks.
