# Improvements from comparing with claude-swap (decided 2026-10-07)

[claude-swap](https://github.com/realiti4/claude-swap) (MIT, Python) solves the
same problem with a different shape: no daemon-grade rotation, no rejection
detector, no Claude Code plugin, identity by email + organization. It has met
several failures the hard way that apply to claudeswitch. We adopt the ideas and
measured facts, not the code, and re-verify each fact locally into
GROUND_TRUTH before relying on it, crediting claude-swap where it came from.

Kept as deliberate differences: no file-store fallback on macOS (DESIGN 4.4),
no identity from `.claude.json` (4.7a, 4.7c), no export/import of refresh
tokens between machines (PROFILES §3), no mid-turn switching by default,
no `-T` on keychain updates (GROUND_TRUTH §41).

## Lane 6 — swap-path correctness (before the multi-profile release)

- **I1. Capture the outgoing credential at every swap.** Daemon and `use`:
  read the live item; if its seat is the outgoing active account, write its
  `claudeAiOauth` to that account's vault entry before overwriting. If it
  cannot be attributed, keep a recovery copy rather than discarding it. Today
  a refresh by Claude Code since the last 2-minute sync means the swap destroys
  the only valid refresh token.
- **I2. Hold Claude Code's credential locks around live writes.**
  `<dir>/.oauth_refresh.lock` then `<dir>.lock` (as read from claude-swap's
  `claude_locks.py`): bounded wait, stale after 60 s, network calls outside
  the lock. Verify the protocol against the installed Claude Code first and
  record it in GROUND_TRUTH.
- **I3. Freshen the swap target.** If its access token expires within ~10
  minutes, refresh it first — refused if it is live in any profile (D18).
- **I4. Keychain line limit.** `security -i` truncates input lines over 4096
  bytes (Claude Code issue #30337, per claude-swap). Measure the command before
  writing; over the limit, **refuse with a clear error** — secrets stay off
  argv (DESIGN 4.2). Exec `/usr/bin/security` by absolute path.

As built (lane 6):

- `vault.SwapToWith` does all of I1 and I2 for the daemon and `use`. The live
  token equal to the outgoing account's vault entry is a free match; otherwise
  one identity lookup (outside the lock) finds the seat. The outgoing seat, or
  another configured account's (`SeatOwner`), files it under that account
  unless the vaulted one is better (the re-add rule: renewable beats
  non-renewable, later refresh expiry wins). Anything else goes to a recovery
  item, `claudeswitch-recovery-<profile>-<1..5>`, oldest replaced; with no
  slot readable the swap aborts. Recovery names are their own kind in
  credstore: a keychain item on macOS, and on Linux
  `~/.claude/claudeswitch/recovery/<profile>-<n>.json` (0600 in 0700), never
  the live `.credentials.json`. A credential that cannot be saved is never
  overwritten. A live credential that is already the target's (same token, or
  same seat under a newer token) is kept, the newer one captured into the
  target's entry, and nothing is written. Before the first write the swap
  checks that both the new blob and the snapshot it would roll back to fit the
  store (the 4032-byte line), so a rollback is never refused.
- Locks: `internal/cclock`, protocol in GROUND_TRUTH §43. Waits 5 s; a busy
  lock is `vault.ErrLockBusy`, which the daemon treats as "retry next tick"
  and `use` as "try again in a moment". The live credential changing while it
  was being attributed restarts the swap (at most three times). Rollback
  re-takes the lock and never overwrites a credential that changed after the
  swap. `refresh` of a live account holds the locks from the token exchange to
  the live write, as Claude Code does.
- I3: `freshenTarget` refreshes a target expiring within 10 minutes, only when
  the §3 check passed and the target's seat is known absent from this
  profile's item (`HoldsAccount`, not a token comparison).
  A failed refresh is logged and the swap goes ahead.
- I4: the limit is Claude Code's own, **4032** bytes for the whole line, not
  4096 (GROUND_TRUTH §43).
- Tests in every package that could reach `security` or `claude` run behind
  `internal/testshim`, which fails the run if either is invoked.

- **I1a. Recovery tooling (decided 2026-10-07).** `cs recovery` lists the
  unattributed credentials kept during swaps (profile, when, seat if it can be
  identified, expiry); `cs recovery restore <slot> <account>` vaults one under
  an account after verifying its seat and clears the slot. `doctor` mentions
  non-empty slots. Lands after lane 6, before the multi-profile release.

As built (lane 7):

- `cs recovery` reads every slot of the configured profiles, plus
  `default`'s and the unnamed slots, and lists what each item records: when
  it was kept, the seat if it was identified then (and the configured account
  owning it), access and refresh expiry, whether it is renewable. No network,
  no token printed. `--identify` makes one identity lookup per slot, through the
  shared budget at interactive priority. An unreadable slot is listed, not
  skipped.
- `restore <slot> <account>`: the account must be configured; its pin (else
  its vaulted entry's seat) is what the credential's seat must equal, and with
  neither it is refused. The seat comes from an identity lookup, or from the seat
  recorded with the item when that call fails; `--force` never skips this
  check. The re-add rule then applies unless `--force`; the entry keeps its
  annotation; the slot is cleared after the vault write.
- `clear <slot>` asks, or needs `--yes` with no terminal. `doctor` prints
  `[warn] recovery` with the count and the oldest.
- A swap that names no profile used to share `default`'s slots; it now has
  its own (`claudeswitch-recovery-<n>`, slot id `<n>`), so a declared
  `default` profile never shows another Claude Code's credential. Items an
  older build kept as `default-<n>` are listed under `default`.
- Hardening from the lane 6 review: account ids and profile names are plain
  names (letters, digits, `.`, `_`, `-`; no leading `-` or `.`; no `..`;
  1–64 characters), refused by config validation and by `add`/`login`/`rename`
  before anything is vaulted. Keychain writes refuse a name with a control
  character, and `escapeForSecurity` replaces any that still arrive, so a
  `security -i` line can never be split. A live refresh checks that its
  write-back fits the store before the token exchange. Switching back after
  `login` passes the seats to the swap like every other swap path.

## Onboarding — on main, shipping as 0.4.9

claude-swap's flow is: sign in in Claude Code, `cswap add`. Ours took four
steps and two failures to add one account on 2026-10-07. Keep seat
verification, `--direct`, `--browser` and `--sso`; remove the busywork.

- **O1. `login <id>` writes its own config block**, pinned to the seat it
  verified, and appends the id to `priority`. An unpinned block gets pinned.
- **O2. `add` without a name** suggests one from the live account; re-adding a
  seat vaulted under the same name refreshes it in place.
- **O3. The daemon reloads `config.toml` on change**, keeping the last good
  config when an edit is invalid.
- **O4. `add-token <id>`** vaults an account from a `claude setup-token`
  token (stdin preferred), for machines without a browser.

## Auto-switch behaviour (decided 2026-10-07, after lane 5, on multi-profile)

- **A1. Landing margin.** A switch target must have at least `landing_margin`
  points (default 10) of room below its own trigger, so a switch never lands on
  an account about to be rotated away from. Policy only; per profile via
  `ForProfile`. It applies to the 5-hour window only: for the weekly window
  any account under its trigger stays a valid target, because 98% → 99% leaves
  no room for a margin. When only in-margin targets exist it stays until the
  hard floor or a refusal, then lands on the best of them.
- **A2. Failover when blind.** If the active account's usage is unreadable for
  `blind_failover_polls` consecutive polls (default 3), and it is not simply an
  expired access token on an idle session (Claude Code refreshes that on the
  next message), switch to a healthy account. "Blind" means the failure
  streak itself has lasted N × `poll_active`, not merely that the last reading
  is old; 429s never count (GROUND_TRUTH §42); a stale vault token never
  counts against a working live one; an expired token is exempt only when the
  session is idle; and the failover waits for an idle gap — it is never forced
  mid-turn. Amends DESIGN 4.4's "hold on
  unknown" for the case where holding has stopped being safe.

## macOS menu-bar app (decided 2026-10-07, in parallel)

- **M1. Native SwiftUI `MenuBarExtra` app in `macos/`**, versioned with the
  binary. It reads `state.json` directly plus `why --json` / `config --json`
  (`status --json` reads the keychain, so the app never runs it) and acts only
  by running `claudeswitch use`; it never touches the keychain, the usage API or
  the daemon's lock.
- **M2. Shared without an Apple Developer account.** Primary:
  `./install-app.sh` builds it on the user's Mac with the Command Line Tools,
  so it is never quarantined and opens without warnings. Secondary: an unsigned
  zip on releases, with the one-time "Open Anyway" steps. Signing and
  notarization ($99/yr) can be added later without changing the app. *Not now (owner, 2026-10-08).* Not the
  App Store: sandboxing would forbid running the binary and reading its state.

## The app manages everything (decided 2026-10-07)

- **M3. Settings, profiles, accounts and the daemon are all managed from the
  menu-bar app**, still only through the `claudeswitch` binary (M1): every
  `cs config` setting with the CLI's validation; profiles (create, seed, pool
  and per-profile threshold edits, forget ghosts); accounts (rename, scope,
  priority order, pin, re-login, recovery copies); the daemon (live/dry-run,
  restart, install, launch at login).
- **M4. Adding an account offers both routes**: browser sign-in with the code
  pasted back into the app (`login --direct`, live session untouched), and
  saving the login Claude Code is signed into now (`add`).
  *2026-10-20:* the sheet's Profile row is now Pool (PROFILES D37: `--pool` /
  `--no-pool`), and the browser route gains Organization (`--org`,
  GROUND_TRUTH §48).
- **M5. Deleting an account removes everything**: vault credential, the
  `[[account]]` block, its pool and priority entries and its state record,
  after the one-credential check and a confirmation naming the account and
  seat. It works while the daemon runs (decided 2026-10-07): the config edit
  goes first, the daemon's hot reload stops using the account, liveness is
  checked again, and only then is the vault item deleted; if the account went
  live meanwhile the config edit is put back. Pinning applies to the account a
  profile is using now.
- **M6. Every app action has a machine-readable CLI form** (`--json` in and
  out, no prompts), so the app never parses human text.

- **M7. Look: native macOS, refined; the popover is mockup 1, "profile
  cards"** (canvas: https://claude.ai/artifact/28QCZzoNdJGvhoPRXnsspM). A card
  per profile with session/week bars showing the switch threshold, an account
  picker, "Open Claude Code" and "Switch account"; the profile the menu-bar
  title follows is highlighted. Management lives in a Settings window with a
  sidebar (Profiles, Accounts, Rotation, Polling, Daemon, Advanced). Switching
  covers a profile's account, launching Claude Code in a profile, and choosing
  which profile the menu bar follows.

As built (lane 14, M3-M7): the app acts only through docs/APP_CLI.md (plus
`chrome` from lane 13) and needs a binary of 0.5.1 or later (dev builds
pass). The followed profile is kept in the app's defaults; the terminal for
Open Claude Code is a setting (Terminal by default, or iTerm, Ghostty, Warp).
Deleting asks the CLI first and uses its `confirmation_required` message,
which names account and seat, as the confirmation text. The global settings
are grouped into Rotation, Polling and Advanced by the app (the schema has
no grouping; an unknown key lands in Advanced). Not built, for want of a CLI
form: removing a profile, an account's plan and current scope in the
Accounts list (email comes from state.json's `emails`). Since lane 15
both have one — `profile remove` and `account list`'s `plan` — and the app
uses them; scope went with S1.

## Claude in Chrome (investigated and decided 2026-10-07)

The extension holds its own OAuth pair (claude.ai authorize, client
`dae2cad8-…`, scopes `user:profile user:inference user:chat`), kept in its own
`chrome.storage`, and silent re-auth pins it to the stored account. Claude Code
does not hand it a credential: both meet on a bridge channel keyed by account
uuid (`wss://bridge.claudeusercontent.com/chrome/<accountUuid>`), Claude Code
deriving its uuid from its live token. So after a rotation Claude Code looks
for a browser on the new account while the extension stays on the old one
(inferred from the code; untested). Writing the extension's storage or Chrome's
cookies is ruled out.

- **C1. One Chrome profile per account, plus a notice.** `cs chrome add
  <account>` creates a Chrome profile inside the default user-data dir (so the
  native-messaging host manifests still apply), opens it at the claude.ai login
  and the extension's page; `cs chrome <account>` opens it. After a rotation, and
  when browser tools fail with the same-account error, claudeswitch says which
  account's profile Claude in Chrome needs. The app gets an "Open Chrome for this
  account" action. Automatic re-routing after a switch was confirmed on a real
  machine on 2026-10-08 (GROUND_TRUTH §45).

- **C2. Use your own Chrome profiles: one per profile, overridable per account
  (decided 2026-10-08).** The owner wants the app and the CLI to use Chrome
  profiles he already has, not new ones: one for `default`, one for `work`.
  The extension's login still cannot be moved (above), so per-profile routing
  cannot follow a rotation by itself; per-account mappings can.
  - **Config:** `[[profile]]` gains `chrome = "<Chrome profile folder>"`, set
    with `cs profile set <name> chrome <folder|name|inherit>` and stored as the
    folder (`Default`, `Profile 2`). An account's own mapping (state.json
    `chrome_profiles`) can now point to an existing Chrome profile:
    `cs chrome add <account> --existing <folder|name>`. Without `--existing`,
    `add` still creates one.
  - **Resolution** for the account live in profile P: the account's own
    mapping, then P's `chrome`, then Chrome's last-used profile. claudeswitch
    never creates a Chrome profile unless asked.
  - **After a rotation** in P to account A:
    - if A has its own mapping, the existing notice applies, and routing is
      automatic (§45);
    - if only P's `chrome` applies, the daemon, `cs use` and the app say
      `Claude in Chrome in "<name>" is still signed in as <old>; sign it in as
      <A>`, with an action that opens that Chrome profile at the claude.ai
      login and the extension's sign-in.
  - **Reading Chrome (owner decision, 2026-10-08):** claudeswitch may read
    Chrome's `Local State`, read-only, for the profile list only
    (`profile.info_cache` folder → name, and `profile.last_used`). It never
    reads cookies, preferences or extension storage, and never writes
    anything of Chrome's. `cs chrome profiles [--json]` lists them; on Linux
    it reads the google-chrome and chromium paths.
  - **App:**
    - Settings → Profiles gets a "Chrome profile" picker per profile (names
      from `cs chrome profiles --json`; "Chrome's last used" by default).
    - Settings → Accounts gets a per-account "Chrome profile" choice: same as
      the profile, one of yours, or create a new one.
    - The popover card shows the sign-in notice with its button.
    - The JSON forms are additive in docs/APP_CLI.md.

- **I5a. Cadence trade-off (decided 2026-10-07).** An account refills ~28
  calls/h after Claude Code's own reads, so the active account cannot have both
  2-minute routine reads and a quickly rebuilt hot reserve. Chosen: routine
  reads every 3 minutes (default `poll_active` 3m; ~8 calls/h spare rebuilds
  the reserve), stale-decision warning at 4 minutes so routine operation never
  warns, hot spells at `poll_hot` 60s. A second spell soon after the first may
  run a few hot reads slower while the reserve refills.

As built (lane 12, M3–M6; the contract is `docs/APP_CLI.md`):

- `--json` on config (`get`, `set`, and `config schema` generated from the
  same settings table, with defaults from `config.Defaults`), profile
  (`create`, `seed`, `list`, `forget`, `pool`, `set`), account (`rename`,
  `delete`, `pin`, `unpin`, `priority`, `scope`), `priority`, `remove`,
  recovery (`list`, `restore`, `clear`), `use`, `add`, `login --direct` /
  `--code` and `daemon`. Failures print `{"error":{code,message,hint}}`
  with exit 1; `--json` or `--yes` turns every prompt into a refusal
  (`confirmation_required`, `name_required`).
- New operations: `profile pool <p> add|remove <id> [--to <q>]` (D1: add
  refuses an account another pool lists; `remove --to` moves in one edit;
  §3 refuses an account live in the profile it leaves or anywhere but the
  one it joins); `profile set <p> <key> <value|inherit>` for the five
  per-profile keys (`models none` = empty list); `priority <id...>`;
  `account scope`; `account pin`/`unpin` (state's existing `pinned`, which
  nothing set before; only the live account can be pinned).
- M5: `remove` / `account delete` removes the credential, the block, its
  pool and priority entries and its state records, after the live check
  across profiles and ghosts (D18: unknown is live), with the daemon
  stopped; the config edit is put back if the credential delete fails.
- M4: `login --direct --json --no-open` prints the URL, expiry and the
  pending record; `login --code --json` and `add --json` print the vaulted
  account. A wrong-seat `--code` now exits 1 (`wrong_account`) instead of 0.
- `daemon status|start|stop|restart|live|dry-run|install|uninstall`
  writes the same plist/unit as install.sh (a test holds them equal) for
  the running binary. install.sh's plist heredoc ran `security` through an
  unescaped backtick pair in a comment; the backticks are escaped now.
- Config edits made by `config set` are textual now (they used to
  regenerate the whole file, dropping comments and replacing a symlink).

Lane 12 security review (owner decisions):

- **Deleting an account works while the daemon runs** (reversing the
  build's refusal). Liveness is re-checked after the confirmation and just
  before the credential goes. With no daemon the delete holds the daemon
  lock throughout; with one, the config edit goes first and the credential
  only once the daemon runs the config the delete wrote (re-review: state
  `daemon_config_hash`, the content hash of the config the daemon loaded,
  written at start and on every reload; the seed wait uses it too, so a
  marker from an earlier run never counts; 10 s wait). A timeout or a
  newly live account puts the config back — compare-and-swap, so a
  config changed meanwhile is left alone (`config_changed`) — and keeps
  the credential. `vaulted` in state is now the CLI's alone: a daemon save
  keeps the disk's list, a CLI save applies only its own adds and drops,
  so a daemon's start-time copy no longer brings a deleted id back.
- **Pin only the live account** (as built).
- **`daemon install` refuses a non-durable binary** (AppTranslocation,
  temporary directories, `go run` builds), with a hint to install it first.
- Minors: NaN/Inf refused by the setters and by `Validate`; unit paths
  escape `%`/`$` and paths with control characters are refused;
  launchctl/systemctl by absolute path; the twin delete clears the
  deleted name's live record; a bad flag with `--json` answers with the
  error object; the service temp file is removed when its rename fails.
  Re-review: `cs uninstall` also runs launchctl/systemctl by absolute
  path, and the `go run` check matches a `go-build*` path component.

As built (lane 13, C1):

- `cs chrome add <account>` runs `open -na "Google Chrome" --args
  --profile-directory=claudeswitch-<account> <login> <web store>` on macOS
  (an argv, never a shell), `google-chrome`/`google-chrome-stable`/`chromium`/
  `chromium-browser` with the same flag on Linux, and refuses elsewhere
  (`unsupported_platform`). The directory name passes `config.ValidName`.
  The mapping is in state.json (`chrome_profiles`), merged like ghosts so a
  daemon save never undoes a CLI add or forget; Chrome's files are never
  read or written. `cs chrome [<account>]`, `cs chrome list --json`,
  `cs chrome forget`. JSON forms: docs/APP_CLI.md (chrome).
- After a switch (owner decision): to an account with a profile, the daemon
  logs and notifies, and `cs use` prints, `Claude in Chrome: use the <to>
  Chrome profile (cs chrome <to>)`; to one without, when any other account
  has a profile, `Claude in Chrome: <to> has no Chrome profile — cs chrome
  add <to>`; nothing when no account has one.
- The email `add` names is `emails` in state.json, recorded by `add`,
  `login`, `setup` and `identify` from the vault entry they just wrote; `cs
  chrome` never reads the keychain (owner decision). `cs rename` moves the
  mapping and the email. A directory read from state is revalidated before
  it reaches Chrome's argv.
- The plugin's PostToolUseFailure hook (only; review: a successful call's
  output is page content) on `mcp__claude-in-chrome__*` runs `claudeswitch
  chrome hint`: when the `error` says "same claude.ai account" or "not
  connected" it tells Claude the account Claude Code is on and the command,
  once per rotation of that profile (`chrome_hints` in state.json).
  `claude plugin validate` accepts it (Claude Code 2.1.293).
- **Untested:** that Claude Code then reaches the right profile's extension
  by itself, and whether the two error phrases are the extension's exact
  wording. Verified only with the test shim's fake launchers.

As built (C2):

- `[[profile]] chrome = "<folder>"`, `cs profile set <p> chrome
  <folder|name|inherit>` (a name resolves through Local State; unknown names
  are refused with the list), shown by `cs config` and `cs config get chrome
  --profile P`. There is no global `chrome`: with no `[[profile]]` blocks,
  `profile set default chrome` is refused with a hint to use per-account
  `--existing` mappings.
- Local State (chrome_local.go) is read through one seam,
  `chromeLocalStatePaths`, decoding only `profile.info_cache.*.name` and
  `profile.last_used`; missing or unparseable is "no list".
- `cs chrome profiles [--json]`; `cs chrome add <a> --existing
  <folder|name>` (state `chrome_profiles.*.existing`; creates and opens
  nothing; `add` without it replaces an existing-profile mapping with a new
  one); `cs chrome signin [<a>]` (opens the claude.ai login and the Web Store
  page: the extension's own sign-in page has no known URL).
- One resolution (`resolveChrome`): account mapping, profile `chrome`,
  Chrome's last used, else `none`. `cs chrome [open]` uses it, so an account
  without a mapping opens its profile's or the last-used Chrome profile
  instead of being refused. The plugin's hint names `cs chrome signin` when
  the profile rule applies.
- After a rotation where only the profile's `chrome` applies, the daemon
  (log and notification) and `cs use` say `Claude in Chrome in "<name>" is
  still signed in as <old>; sign it in as <new> (cs chrome signin <new>)`
  (when the old account had its own Chrome profile, "may be signed in as
  another account" instead). State records `last_from` with `last_switch`.
- App: Profiles pane picker, Accounts ⋯ "Chrome profile" menu, the card's
  amber notice with "Sign in as <new>" (dismissed per rotation, remembered in
  the app's defaults). "Open Chrome" no longer creates a Chrome profile.

## Scope removed (decided 2026-10-08)

- **S1. Remove account `scope` and `[[project]]` rules entirely.** Scope only
  ever acted through `[[project]]` directory rules (eligible/prefer by working
  directory); profiles now do the real separation (an account belongs to one
  Claude Code setup). A config that still has `scope` or `[[project]]` loads,
  with a warning that they are ignored and how to delete them; `cs config`
  single-line edits leave them as written (amended in lane 16: no silent
  rewrite), and `cs config clean` deletes them. The CLI (`account scope`,
  `--scope` on add/login), JSON output, the app (scope column and pickers),
  policy (`scopeRank`, `ScopeAllowed`, project matching), README and docs
  lose it.

As built (lane 16, CLI):

- Config: `Account.Scope`, `Config.Projects`, `ProjectFor`/`ScopeAllowed`
  and the project validation are gone; `[project.…]` tables (the form the
  config used) and `scope` lines are read only to be reported
  (`config.Legacy`). Commands that load through the shared loader (status,
  why, plan, use, add, login, watch…) print one `warning:` line to stderr
  naming the accounts with scope lines and the number of project tables;
  the daemon's stderr is its log, and a hot reload logs it again only when
  an edit changed it; `doctor` lists it as a `[warn] config` row.
  `config set` and the other textual edits leave the lines alone.
  `cs config clean [--yes] [--json]` removes them (each project table with
  its comment lines and the blank line before it), parse-back checked.
- Policy: `Input.Dir` is gone with the project rules, and with it the
  CLI's working-directory plumbing; `why --json` and `plan --json` no
  longer carry `dir`, and `plan` no longer prints `here/allowed/prefer`.
  Selection is room, then priority.
- `account scope` is an unknown verb (`usage`); `--scope` is an unknown
  flag on `add` and `login`; no JSON object carries an account `scope`
  (`account list`, `accounts --json`, `status --json`, `login --direct`'s
  `pending`, `login --code` / `add`). New blocks are written without one.
  The pending-login record no longer stores one (an old record's is
  ignored).
- `add --from <profile>` (owner decision B2, for the app's two pickers):
  the live credential is read from `--from`, the account joins
  `--profile`'s pool; the suggested name comes from the source.
- `version --json` → `{"version", "contract": 2}`, the app contract's
  version (docs/APP_CLI.md).
- `why --json` names each profile's `best` manual-switch target
  (`policy.Best`: the chooser's own ranking, landing margin on the 5-hour
  window included, pin and cooldown ignored) and `best_why` when there is none.

As built (lane 16, app):

- B1 (the owner's report: a pool edit "undone"): the app's data lives in
  `AppData` (Core). Each refresh is numbered when it starts and carries only
  what it read; a part is applied only when nothing newer — a later read,
  or an edit's answer — was applied since. A pool edit applies the CLI's
  `pools` at once. Tested with a stateful fake binary whose `profile list`
  answers late with the pools from before the edit (QA's QARace).
- B2 Save current login has two pickers, source (`--from`) and target
  (`--profile`); the suggested name is asked again per source and a late
  answer is dropped. B3/B10: an existing account's profile is shown
  locked ("already in X. Move it in Profiles."), `--profile` is not sent
  for it, and a `pending.profile` that differs is said, never hidden.
  (*Since shared pools, 2026-10-20:* the row reads "Used by", every profile
  that may use it, and the account stays in its pool.)
  B4: accounts another profile lists are moved in after create, confirmed
  first; D6-only ones join at create. B5: a refresh follows failures too;
  pool edits say "X is now in Y" / "already in Y"; a `profile list` error
  object shows in Profiles. B6: the remembered rotation order is dropped
  on every account-list read. B7: the implicit profile says "No profiles
  yet" and offers New profile instead of editors. B8: the app requires
  `version --json` contract 2 (a pre-lane-16 dev build is refused). B9:
  the sheet starts on the first declared profile.
- C: cards keep the picker; the second button is "Switch to best" with the
  id and figure on a second line, disabled with `best_why`. Accounts rows
  get "Move to profile ▸", confirmed like the Profiles pane; one verb,
  "Move to X", for listed and D6 accounts. (*Superseded 2026-10-20:* rows
  move between pools, "Move to pool" or a drag onto another pool; PROFILES
  §10.)
- D: QA's V1–V22 (status-first summaries, status chips with D6 members
  dashed, coloured model rows with a bar, popover capped to the screen's
  visible height, truncation with tooltips, setting labels and durations,
  unsigned profiles cannot open Claude Code and get "Sign in with", Move
  up/down for VoiceOver, Remove-profile sheet without a destination for an
  empty profile, "accounts" not "pool"). `--render … --now` fixes the clock;
  `macos/scripts/render.sh` renders the base, `many` and `one-profile` sets
  (macos/RenderFixtures, from scripts/gen-render-fixtures.py).

## App polish after live use and QA (decided 2026-10-08)

- **M8. "Save current login" takes two profiles**: whose live login to save
  ("the login Claude Code is using in [profile]") and which profile's accounts
  it joins. The suggested name follows the source. (*Superseded in part
  2026-10-20:* the second picker is the pool it goes into, "Add it to pool",
  PROFILES D37.)
- **M9. A card's second button is "Switch to best"** (the account with most
  room in that profile); the account picker stays for choosing a specific one.
- **M10. Accounts rows get "Move to profile"**, with the same confirmation and
  checks as the Profiles pane; one verb, "Move to X", for every move.
  (*Superseded 2026-10-20:* accounts move between pools, "Move to pool" or a
  drag onto another pool's header, confirmed first; PROFILES §10.)

## Later lanes — experience

- **I5. Re-measure the usage-endpoint limit, then redesign the cadence.** The
  daemon log shows daily 429s (243 on 09-28, 323 on 10-01, ~25/day since) and
  stale-reading decisions. claude-swap measured ~30 calls per trailing hour per
  identity, against our 20 s hot polling (~180/hour). The probe deliberately
  trips the limit (up to ~1 h blind on the probed account) and must be
  scheduled. Then: per-account hourly budget, movement-driven cadence, backoff
  after a 429, no 20 s hot polling. Touches DESIGN 4.3/4.3b, GROUND_TRUTH §34,
  PROFILES D3/D15/D16.
  *Built (lane 11, 2026-10-07) from §42 without the probe*: per-account
  allowance (20, refilling 1 per 2 min, reserve 3), movement-driven hot
  polling, 5→20 min backoff ignoring `Retry-After: 0`, `poll_active` default
  3m (owner; stale-decision cap 4m), a faster one loaded at 2m with a warning, `Write` no longer
  pinning defaults; see DESIGN 4.3c. The sustained-rate probe is still unrun and would
  tighten the refill figure.
  *Probe not planned (owner, 2026-10-08)*: today's figures are working, and
  the probe would make an account unreadable for up to an hour.
- **I6. Per-model weekly limits.** Parse `limits[]` entries of kind
  `weekly_scoped` with `scope.model`; show them in `status` and the status
  line; optional policy input. Capture a real response first; an unknown scope
  is unknown, never fine. Fix the contract test to watch `limits[]`.
- **I7. `profile create` / `run`.** One command makes a profile (dir, shared
  settings/skills symlinked, user MCP servers mirrored, a credential seeded for
  Claude Code to adopt, `[[profile]]` written); `run` launches `claude` in it.
  Must keep §3: the seeded account is one live nowhere else.
  Built in lane 10: `cs profile create`, `cs profile list`, `cs run`
  (PROFILES.md §8, "As built (lane 10)").
- **I8. Weekly pace view.** Expected vs actual weekly %, and "~X% will expire
  unused at reset". Display only — no change to room-first selection.

As built (lane 9, I6 and I8):

- `usage.Limit` keeps the API's shape: `percent` is a pointer (null or missing
  is unknown, never 0) and `scope` is stored as sent (`model.id`,
  `model.display_name`, `surface`), so state.json carries the model name the
  macOS app reads. Each entry is classified: `session`, `weekly_all`,
  `weekly_scoped` with a named model and no surface (per-model weekly), or
  unknown. Unknown entries are shown as unknown and never counted. The
  contract test now fails on any entry it does not understand, instead of
  watching the obsolete `seven_day_opus`/`seven_day_sonnet` keys.
- Policy input `models = [...]` (global, and per profile; nil inherits, `[]`
  turns it off): a counted model's limit stands in for the weekly window when
  it is higher, with its own reset, through `state.Account.WithModels`; the
  copy's previous reading is shifted by the same amount so the burn rate is
  unchanged. An account with no limit for a counted model is judged on its
  weekly window. A counted limit present without a figure makes the account
  unknown: never a target, and an active one holds (DESIGN 4.4). Reasons name
  the model ("over the 95% Modelname weekly trigger"). Default empty: today's
  behaviour exactly. The poller's hot cadence still reads the unscoped figures.
- Shown in: `status` (a WEEKLY BY MODEL block, only when an account has one;
  unknown kinds in WARNINGS), `status --detail` (one line per model limit,
  "counted" when it is), `why` (a WEEKLY section), `plan` (the active
  account's model limits, only when it has any), the status line (only a
  model limit marked active or at `switch_at_weekly`, as `Modelname 96% 2d`),
  JSON (`model_limits`, `unknown_limits` on `status --json` and `why --json`
  accounts, additive) and the app (unchanged: it already read the scope).
- Pace: `state.Account.WeeklyPace`. Expected = share of the 7-day window
  elapsed (from `resets_at`); actual = weekly utilization, brought up to now
  by the existing projection when the weekly window is the one burning. The
  estimate at reset extrapolates the window's **average** rate so far, not the
  short-term burn rate (one hot hour would project across days), and only
  from 24 hours into the week. Shown in `status --detail`, `why`, JSON
  (`weekly_pace`: expected, actual, resets_at, and at_reset/unused_at_reset
  once estimable) and the app's popover; never in the compact status or the
  status line.

Not chosen for now: argv fallback for oversized writes, a use-it-or-lose-it
selection strategy, `oauthAccount` splicing, `service install` from the binary,
`schemaVersion` on JSON, session records as the busy signal.

## Refusal wording when a safety check is rate limited (decided 2026-10-08)

- **R1. Say when to retry.** When `use` (or any swap) is refused because the
  cross-profile check could not get an answer and the cause is a rate-limit
  lock on the token it needed, the refusal keeps refusing but names the time
  the lock clears: "can't confirm X isn't signed in under P: the check is rate
  limited until HH:MM:SS; try again then". Other unknowns keep today's wording.

- **M11. No modal alerts in the popover (decided 2026-10-08).** A refused
  action's error shows inline in the card that caused it, with a dismiss
  button and, when the CLI gives one, the retry time (R1). Observed: a refused
  `use` opened an alert window that macOS never put on screen, leaving the
  popover dimmed and blocked until the app was relaunched. Settings keeps its
  sheets and alerts, which work in a normal window.

- **M12. "Already on the best" (decided 2026-10-08).** When the best other
  account has no more room than the active one (by the binding window), the
  card's second button is disabled and reads "Already on the best", with the
  next best and its percentage underneath. The account picker still switches.

  *Measure (owner, 2026-10-08):* "already on the best" uses the policy's room
  (points short of each window's own trigger, as `policy.Best` ranks), not raw
  utilization, so the button and the CLI never disagree. `why --json` gains,
  per profile, `on_best`, `active_room` and `best_room` (additive), and the
  app reads `on_best`.
  *`daemon install --config` (owner, 2026-10-08):* accepted and ignored; the
  service runs the default config, as install.sh's does.
  *As built (2026-10-08):* `policy.Choose` beside `Best` returns the pick
  with both rooms (the negated exceedance `better` compares; nil when
  unknown) and `OnBest`; `why --json` reports them (null rooms, never 0;
  `on_best` false when either is unknown, with no best, or while the live
  account is refused). The app's `alreadyOnBest` reads `on_best`, falling
  back to the raw higher-of-session-and-week only when the key is absent.
  Fixture `why-m12-room.json` holds the case they disagreed on.

- **`cs session` covers every profile (decided 2026-10-08).** Its span and
  switch list were the default profile's alone, leaving out a switch in
  `work`. Now they merge every profile's switches, each labelled with its
  profile; each message still counts against the account live in its own
  profile when it was written; `--profile P` narrows to one. `--json` adds
  `profiles` and `switch_events` (`switches` stays the count).

- **`cs session`'s default span (owner, 2026-10-08).** Without `--since`, the
  span starts at today's first switch in any reported profile, or 8 hours
  ago if that is earlier (`session.DefaultFrom`). The old rule ("8 hours
  before the latest switch, if later") could never apply, so every span was
  the last 8 hours.

## Next features (decided 2026-10-08)

Chosen by the owner from a list of proposals. Every app feature reads the CLI
(contract 2, additive) and never the keychain or the usage API. The JSON
shapes below are the contract the CLI and the app are built against; the
exact key names may grow, but these keys must exist.

### Rotation

- **F1. Spend expiring quota first (opt-in).** A setting
  `prefer = "room" | "expiring"` (default `room`, today's behaviour), global
  and per profile. With `expiring`, among eligible candidates (the same
  eligibility: under trigger, landing margin, not refused or needing login),
  prefer the account whose weekly window resets soonest *while it still has
  unused weekly quota*. Ties fall back to room. `why` says so: `work-team
  resets in 9h with 40% unused, so it goes first`. `why --json`: per
  candidate `weekly_resets_at`, `weekly_unused`. Settings → Rotation gets the
  choice.

  *As built (2026-10-09, Go side).* `prefer` is a config key (global, and per
  `[[profile]]`), `cs config prefer room|expiring`, `cs profile set <p> prefer
  room|expiring|inherit`, and `config schema --json` lists it as an `enum`
  with scopes global and profile, right after `landing_margin` (the app's
  Rotation group). Written to the file only once chosen, so an untouched
  config writes back unchanged. One rule in `internal/policy`: `better`
  (which bestEligible, `Best`/`Choose` and `why`'s order share) ranks, with
  `expiring`, an account with a known future weekly reset and unused weekly
  quota ahead of one without, the earlier reset (to the minute) first, room
  breaking ties; `room` and unset are today's ranking exactly. `weekly_unused`
  is 100 less the weekly utilization the policy judges (a counted model
  standing in). The switch reason and the first ready account's `why` line
  end `work-team resets in 9h with 40% unused, so it goes first`; `why
  --json` gives every account `weekly_resets_at` and `weekly_unused` (`null`
  when unknown, never 0) whatever `prefer` is. Eligibility is untouched.
- **F2. Pool runway forecast.** From each account's recent burn rate (the
  readings already in state), predict when each account reaches its trigger
  and when the profile's whole pool has no eligible account left. Unknown
  stays unknown (never a guess from too few readings). `cs status` gains one
  line per profile (`work pool: runs dry Thu 14:00 at this pace`), `--detail`
  adds per-account ETAs, and `status --json`/`why --json` add per profile
  `pool_dry_at` (RFC 3339 or null) and per account `trigger_at` (or null).
  The daemon notifies once when `pool_dry_at` falls within 2 hours. The app
  shows the pool line on each card and per-account ETAs in the account
  picker.

  *As built (2026-10-09, Go side).* `policy.Forecast` (beside `Best` and
  `Choose`). The pace is the live account's burn, points a minute, from its
  last two readings, both within 15 minutes (or 3 × `poll_active`) of each
  other and of now; a level pair carries the last rise's rate only while
  that rise is that recent (readings are whole points). State records that
  rise (`rise_rate`, `rise_at`, written by the poller), kept apart from
  `last_rate`, which feeds the projection. The pool is spent as rotation
  would: the live account to its trigger, then each account `best` could
  offer that clears the landing margin, in `better`'s order, at the same
  pace in points (the plans may differ; it is "at this pace"). Unknown is
  `null`: fewer readings, stale ones, a reset between them, or any account in
  the pool with no usable reading. A binding-window reset (or a refusal
  clearing) before the dry time, on an account rotation cannot use now or
  one already being spent, gives room back, so the forecast is then
  `refills` with `pool_refills_at`, not a time past it; a reset of an account
  before rotation reaches it is not counted (it frees only what that account
  used before, so the forecast is early by at most that). Keys, beyond the
  contract's `pool_dry_at` and `trigger_at`: `pool_forecast` (`dry`,
  `refills`, `idle`, `unknown`) and `pool_refills_at`. `cs status` prints
  the pool line only when there is a forecast (no existing golden changed);
  `--detail` says why there is none and adds `↳ reaches its trigger … at this
  pace`. The daemon's notice re-arms only once the forecast moves past 2
  hours or to `refills`.
- **F3. Pin safety valve.** If a pinned account is refused (429, or out of
  quota at 100%) or needs a login, the daemon lifts the pin, rotates as
  usual and says why (`pin on work-1 lifted: it was refused`), in the log,
  a notification and audit (`kind: unpin`). `cs account pin --hard` keeps
  today's behaviour (stay even then). `account list --json`: `pin_hard`.

  *As built (2026-10-09, Go side).* `policy.PinLifted` is the one test:
  refused (a refusal still in force), out of quota (a reading still current
  at 100% on either window) or needs a sign-in (an expired refresh token, or
  a last error only a sign-in cures that was counted as a failed read — a
  401 on a stale vault copy is not counted and lifts nothing). `Decide` with
  a pin that is not hard and a lifted account decides unpinned and sets
  `Decision.Unpin` (`pin on work-1 lifted: it was refused`, also prefixed to
  the reason and in `why --json`'s `decision.unpin`). With the pin lifted,
  rotation is the usual one: an account that needs a sign-in but is under
  its trigger stays until it is refused or runs out. The live daemon clears
  the pin (`ProfileState.LiftPin`, which a daemon save carries to disk unless
  the CLI has pinned something since), logs it, notifies and writes
  `{"kind": "unpin", "profile", "from", "reason"}`; dry run lifts nothing and
  writes the row once with `dry_run`. `pin --hard` stores `pin_hard` in the
  profile's state; `account pin --json` answers `pin_hard`, `unpin` clears
  it. `cs audit` shows the kind and `--kind unpin` filters it.

### Convenience

- **F4. Profile picked by folder.** `[[profile]] paths = ["~/work/**", …]`
  (globs; overlapping matches are refused at load, like an account in two
  pools).
  `cs run` with no name picks the profile for the current directory, else
  `default`. `cs profile which [--json]` says which profile a directory maps
  to. `cs profile hook zsh|bash|fish` prints an optional shell hook that sets
  `CLAUDE_CONFIG_DIR` on `cd` (it never runs anything else). Settings →
  Profiles gets a "Folders" field per profile.

  *As built (CLI, 2026-10-09):* `Profile.Paths` (`config/paths.go`). A
  pattern is absolute or `~/…`, split on `/`; `*` and `?` within a name, a
  whole `**` for any number of folders; `[ ]`, `\`, `.`/`..` and a `**`
  inside a name are refused so that overlap is decided exactly. A folder
  belongs to a pattern when the pattern matches it or a parent (so `~/w`
  and `~/w/**` are the same). Overlap between two profiles' patterns is a
  search over pairs of positions (folder by folder, then character by
  character) and refuses the config at load, naming both profiles and
  patterns; one profile's own patterns may overlap. The folder is tried as
  given, then with links resolved. `profile which [--dir] [--json]` →
  `{dir, profile, pattern, rule: paths|default|none, config_dir, from_env}`;
  `run` with no name starts the folder's profile (a note names the pattern),
  else `default`, else refuses. `profile hook zsh|bash|fish` runs
  `claudeswitch profile which --hook` (one line: `set <dir>`, `unset`,
  `keep`) on each change of folder and only sets or unsets
  `CLAUDE_CONFIG_DIR` from it, never evaluating the answer; `keep` where
  nothing is picked, with no `[[profile]]` blocks, or when the config does
  not load. The bash hook is run under bash in a test; zsh and fish are
  checked as text. `profile list --json` gains `paths` (`[]` when none);
  `profile set <p> paths <a,b|inherit>` answers with lists. Docs:
  cli/profiles.md, GUIDE "Picking a profile by folder", APP_CLI.
- **F5. Re-login reminders.** When a vaulted account's refresh token expires
  within 5 days (the figure doctor already warns about), the app posts one
  notification per account per day, "personal needs signing in within 5
  days", whose action opens Add account → sign in for that account. Data:
  `account list --json` gains `refresh_expires_at` (from state.json, never
  the keychain; null when unknown).

  *As built (CLI, 2026-10-09):* the key was already there (lane 15), but
  the daemon recorded the expiry only for an account live in a profile; a
  vaulted account kept whatever `add`/`login` wrote, or nothing. The
  scheduled poll reads the vault entry anyway and now records its refresh
  expiry with its access expiry (zero, a credential that reports none, is
  unknown: `null`). Tests: the poller records it, and `account list --json`
  reports it (UTC) or `null` with no keychain read.
- **F6. Shortcuts and a hotkey.** App Intents for Switch to best, Pin/Unpin,
  Status (the followed profile, or a chosen one), usable from Spotlight,
  Shortcuts and Raycast. A configurable global hotkey for Switch to best on
  the followed profile (Advanced → This app). If App Intents cannot be built
  with the Command Line Tools alone, ship the hotkey and a URL scheme
  (`claudeswitch://switch-best?profile=work`) instead and record why.
- **F7. Usage history.** Settings → History charts each account's session
  and weekly utilization over the last 30 days, with switches marked. Data:
  `cs history --usage [--days 30] --json`, a series per account
  `[{at, five_hour, seven_day}]` plus switches, built from what the daemon
  already records (extend the recorded readings if they are not kept; keep
  the file bounded).

  *As built (CLI, 2026-10-09):* state keeps only the last two readings and
  the audit log only the active account's at a decision, so the daemon now
  keeps `~/.local/state/claudeswitch/readings.jsonl` (`internal/readings`):
  one line `{"at","account","five_hour","seven_day"}` per new reading of a
  configured account (handed over after every poll tick and save tick; a
  reading already written is not written again, across restarts too).
  Bounded: 30 days; older than a day thinned to the last reading per
  account per 15 minutes; under 4 MiB, oldest first; compacted at start,
  every 6 hours and when an append crosses the limit, by writing a
  temporary file and renaming it. Only the daemon writes it.
  `cs history --usage [--days 30] [--json]` →
  `{days, from, to, accounts: [{id, profile, configured, series: [{at,
  five_hour, seven_day}]}], switches: [{at, profile, from, to, reason,
  forced}]}`; switches are the audit log's `switch` events. Docs:
  cli/claude-code.md, GUIDE "Usage history", APP_CLI.
- **F8. Homebrew.** *Skipped for now (owner, 2026-10-08): the formula and
  cask stay in packaging/homebrew/; the release workflow step is not applied.*
  Original decision: A formula for the CLI and a cask for the app in a tap
  repository, updated by the release workflow on each tag. The tap
  repository itself is created by the owner (an outward-facing action);
  everything else is prepared in this repo (`packaging/homebrew/`) and the
  release workflow (public mirror).

  *As built (2026-10-09):* `packaging/homebrew/claudeswitch.rb` (a formula:
  the release tarball for each of darwin/linux × arm64/amd64, each sha256
  line marked with its target, `bin/claudeswitch` and the `cs` link, a
  caveat to re-run `daemon install` after an upgrade) and
  `claudeswitch-app.rb` (a cask for `ClaudeSwitch-<ver>-macos.zip`, macOS
  13+, depending on the formula, with the unsigned app's first-launch
  caveat). Both carry `0.0.0` and zero sha256 placeholders.
  `update.sh <version> <checksums.txt> [<formula> [<cask>]]` fills them in
  place (POSIX sh and awk): all four archive sums are required, the app
  zip's line is optional (without it the cask is left alone), and every
  value is checked before anything is written. A Go test in the same
  folder runs it. The release-workflow job (a `homebrew` job after
  `build` and `macos-app`, pushing to the repo the `HOMEBREW_TAP` secret
  names with `HOMEBREW_TAP_TOKEN`, skipped when `HOMEBREW_TAP` is unset) is
  kept out of this repo's change, for the public mirror.

### macOS app

- **F9. First-run setup window.** On first launch (no config), a welcome flow
  that does what `cs setup` does, through the CLI's JSON forms: detect the
  current login and save it, add more accounts (the Add account sheet),
  optionally create a work profile, and install the daemon in dry run.

  *0.6.1 (decided 2026-10-08):* the window no longer writes the empty
  config itself: it runs `cs init --empty --json` (new, `{"path"}`, code
  `exists` when one is there; APP_CLI "init"), keeping the rule that the
  app never writes claudeswitch's files. A binary without `--empty` gets
  a message to update or run `cs setup`. A **Don't show this again**
  checkbox (UserDefaults `firstRunDontShowAgain`) stops the window opening
  by itself at launch; ⋯ → Set up… still opens it.
- **F10. Update checker.** Once a day the app reads the latest GitHub release
  (one unauthenticated API call; none when the check is off in Advanced).
  When newer, the popover footer shows "0.5.x available" with Update, which
  downloads the app zip and the binary for this Mac, verifies both against
  the release's sha256 files, installs them where the current ones are, and
  restarts the daemon and the app. A failure leaves everything as it was.
- **F11. Notification actions.** Rotation notifications carry **Undo**
  (switch back) and **Pin here**; a refusal or needs-login notification
  carries **Sign in**.

  *0.6.1 (decided 2026-10-08):* one notification per rotation. While the
  app posts its actionable ones (on, bundled, allowed) it runs `cs app
  heartbeat --json` every minute (new; `app_notifies_until` = now + 2m in
  state.json, CLI-owned, written under the lock), and the daemon skips its
  `osascript` switch notice while that is in the future. Every other daemon
  notice stays; no app, Linux or a quit app notify as before. Go tests for
  both paths; Swift tests for the command and the timer (`AppHeartbeat`).
- **F12. Health pane.** Settings → Health runs `cs doctor --json` (new: one
  object per check, `{name, status: ok|warn|fail, message, fix}` where `fix`
  names a known action: `signin <account>`, `daemon restart`,
  `statusline install`, `keychain allow`, or null) and lists every check,
  each failure with a Fix button for a known action.

  *As built (CLI, 2026-10-09):* the text is byte-identical (goldens in
  `cmd/claudeswitch/testdata/doctor/`, written before the change) and its
  exit status unchanged. `--json` reads that text back: one check per
  `[mark] name  message` row, its `└` lines as `details`; `level` keeps the
  mark (`info` → `status: warn`, so "status line not set" can carry
  `statusline install`). Fixes: `daemon restart` for an older daemon,
  `keychain allow` for an unreadable live credential on macOS,
  `statusline install`, and `signin <id>` on per-account checks the text
  shows as detail lines: `refresh token` (missing, expired, or within F5's
  5 days) and, with `--verify`, `credential`. `failed` counts the `[FAIL]`
  rows; the JSON form exits 0 whenever it made the report (an exit 1 is an
  error object to the app).

  *0.6.1 (decided 2026-10-08):* a check with a known `fix` gets its button
  whether its status is `warn` or `fail`, so the "status line not set" info
  row (`warn`, level `info`) gets **Install the status line**. An unknown
  fix or an ok row still gets none.

### Fixes (0.6.1, decided 2026-10-08)

- **`last_rate` was always 0.** The poller took the burn rate while
  `LastAt` still equalled the `PrevAt` it had just set, so the pair spanned
  no time. It now moves `LastAt` first. *Idle accounts don't project:* now
  that `last_rate` is real, a flat pair would carry it forward for an
  account nobody is using. Only some profile's live account, or an account
  read within `poll_active`, shows a projected "~" figure, a burn rate or
  `burn_per_min`/a raised `projected` in `status --json`
  (`state.Projects`/`ProjectedFor`); the policy already projects only the
  active account. No golden changed.

## 429s after the 0.6 releases (investigated and decided 2026-10-09)

A read-only investigation of the daemon log, audit, ledger and readings
found two causes. (1) An idle profile's live access token expires (no
Claude Code session renews it), and the usage endpoint answers an expired
token with **429**, not 401, so the daemon called it every 20 minutes for
hours (25 strikes for work's work-team on 10-08/09; the same for
another work-profile account on 10-08). (2) The account in use was really rate limited
when several busy Claude Code sessions (which call /api/oauth/usage
themselves) added to claudeswitch's ~28 reads an hour at poll_active 2m.
First strikes of 20m mean the server sent a long Retry-After, which our
20m lock cut short, so the next call was a guaranteed second strike.

- **R2. Renew an idle profile's expired live token (owner, 2026-10-09).**
  When a profile's live access token is expired (or within the refresh
  window) and no Claude Code session in that profile is busy (the transcript
  check, D15), the daemon refreshes the live item itself, holding Claude
  Code's credential lock as RefreshIn already does, and re-reads. A token
  past its expiry is never sent to the usage API: until it is renewed it is
  parked with no strike. Owner accepted the costs: a keychain write, and a
  session that is open but not busy loses its token.
  *As built (2026-10-09):* `usage.Expired` is the test (a zero expiry is
  unknown and still sent). The poller (`fetchInto`, which now takes the
  token's expiry, for scheduled, candidate, `status` and live reads), the
  vault's seat probe (`HoldsAccountWhy`), `Verify` and the swap's verify
  (`fetchCred`) never send an expired token: `ReasonExpired` /
  `usage.ExpiredError`, no call, no strike, no blind-failover count, and
  `last_error` "not sent: the access token expired at HH:MM; parked until it
  is renewed" (a needs-login error already there is kept). A parked account is
  looked at again every minute (`ParkRecheck`, a local read). A swap that
  installs an expired token stands unverified. The daemon's renewal
  (`renewQuietLive`) runs on the two-minute vault tick after the re-capture
  step: a profile whose live token (read from its own item) is expired or
  within `refresh_window`, whose active account is vaulted, and which is not
  busy (D15, `daemon.busy`) gets `RefreshIn(…, il.live, allowActive=true)`,
  then `PollActiveIn`; at most one attempt per profile every 10 minutes;
  audited `kind: refresh` with `profile`, `account`, `reason` and `error`;
  logged "renewed a quiet profile's live token". A `NeedsLoginError` writes
  the account's `last_error` (status and the app's banner read it), saves,
  and sends the `relogin:<id>` notification. Gated on `auto_refresh` like the
  idle refresh, and runs in dry-run like it. Not done: a live account with no
  vault entry is left parked (RefreshIn renews through the vault). The
  capture-time identity lookup in a swap (`seatForCapture`, profile endpoint)
  still asks with whatever token is live, since an unknown seat there only
  files a recovery copy.
- **R3. Back off longer after a repeat 429 (owner, 2026-10-09).** On a second
  long-wait 429 for the same token: 40m, then 60m for idle accounts; 20m then
  30m for the account in use. The server's Retry-After is logged again.
  *As built (2026-10-09):* `usage.LockFor`. A long wait is a `Retry-After`
  over `MinBackoff` (5m); the ledger's lock counts them (`long`) beside
  `strikes` until the next success. Each is honoured up to its step —
  `LongWaitSteps` 20m, 40m, 60m (the last repeats) for an idle account,
  `LiveLongWaitSteps` 20m, 30m for a credential marked live — and never less
  than the doubling backoff. A Retry-After at or under 5m (§42's burst limit
  sends 0) keeps the doubling schedule: 5m, 10m, 20m (10m live). A server
  asking less than the step gets what it asked. `MaxLock` is now 60m (the
  watchdog ignores locks, so it no longer bounds them). The poller's and the
  vault's 429 warnings carry `retry_after`.
- **R4. Fewer calls on idle profiles and at restart.** An idle profile's
  live account is read at poll_idle, not poll_active; a restarted daemon
  starts each account's schedule from its last reading.
  *As built (2026-10-09):* `Poller.quiet` is D15's busy test, false when no
  test is wired. A quiet profile's live account is scheduled at `poll_idle`,
  is never overdue at `poll_active`, and is read at Scheduled priority; with
  one profile it still polls hot while moving within reach (D16). It applies
  with any number of profiles. The daemon's stale-decision warning allows a
  quiet profile `poll_idle` more, and is not given for a parked (expired)
  token. At restart `due` seeds every unscheduled account from `LastAt` plus
  its routine interval, and `ResumeIn` skips a profile's startup read when its
  live token is the recorded `live_key`, unexpired, and its account was read
  within `poll_active`.
- **poll_active back to 3m** on this machine (owner, 2026-10-09).

## Shared pools and choosing the organization (shipped in 0.7.0, 2026-10-20)

- **Shared pools.** Named `[[pool]]` blocks that profiles list, in order;
  profiles that list one pool share its accounts, one profile at a time.
  Supersedes the fixed pool per profile. Decisions D31–D39 and how each was
  built: PROFILES §10.
- **Sign in to a chosen organization.** `login --direct --org <id>` sends
  `orgUUID`, which the authorize endpoint honours; an account signing in
  again asks for its own. The app's Add account sheet has an Organization
  row. GROUND_TRUTH §48; seat attribution within one organization, §49.

## Requests from a SwarmCloud session: rotation and claude.ai identity (received 2026-10-21, not decided)

A long-running SwarmCloud session asked for these, on the owner's behalf,
after a rotation that day. Claude in Chrome then refused every call ("the
OAuth token Claude Code is using belongs to a different claude.ai account"),
and an Artifact the session had published stopped being reachable, because it
sat in the previous account's organization. Nothing here is decided. The
"today" notes say what already exists.

1. **Detect it.** `cs status`, `cs doctor` and the status line show which
   claude.ai account Claude Code presents next to the quota account, and warn
   when the Chrome extension is signed into another (or say "unknown").
   Today: the daemon log and the popover card carry C2's "Claude in Chrome
   in <profile> is still signed in as <old>" notice; the status line and
   `doctor` do not.
2. **A sticky browser identity.** Keep one account as what claude.ai features
   see while rotation moves model traffic. Claude Code derives the bridge's
   account from its live token (C1), so the split looks impossible without
   Claude Code's help. The fallback asked for: never rotate away from the
   browser's account while a session has claude-in-chrome connected, or ask
   first.
3. **Re-pair after a switch.** Open the extension's sign-in for the new
   account, or print the one step that re-pairs it; a `cs browser` command
   for the manual case. Today: C1/C2's notice with its sign-in action, and
   `cs chrome signin <account>`.
4. **Keep CLAUDE_CODE_OAUTH_TOKEN consistent.** claudeswitch never sets it (it
   swaps the keychain item). If something else sets it, `doctor` could warn
   that it and the live credential name different accounts.
5. **A home account for claude.ai objects.** Artifacts and other objects
   published while a rotated account is active land in that account's
   organization. Publish as a configured home account, or warn first.
6. **Record identity changes.** `cs why` and `cs session` note when a rotation
   changed the claude.ai identity, not only the quota account.

A second session (ai-studio-f3, profile default, Chrome profile "1-Alice
Personal") reported the same error the same day, with more evidence:

- **`/chrome` (reconnect) did not clear it**, and nor did `cs use` to any of
  four accounts, `personal` included. `personal` is the account named in
  `~/.claude.json`'s `oauthAccount` (bob@personal.example).
  `CLAUDE_CODE_OAUTH_TOKEN` was not set. `list_connected_browsers` returned
  nothing on every account. The session's guess, unverified: a running Claude
  Code keeps the identity it started with in memory, and a hot swap never
  updates it, so only `/logout` + `/login` or a restart resets it. That would
  contradict GROUND_TRUTH §45, where re-routing after a switch was confirmed
  on 2026-10-08, so it needs a test on the current Claude Code. Asked for:
  bring the recorded login and the bridge in line on every switch, or say in
  `use`'s output, with the exact command, that the person has to do it.
- **Likely bug: the notice names the previous account, not the extension's.**
  Each `use` printed `Claude in Chrome in "1-Alice Personal" is still signed
  in as <x>`, with x the account it had just left. claudeswitch cannot read
  the extension's login (C1), so the notice states an inference as fact.
- **Bug: `cs chrome --help` opened the Chrome profile** instead of printing
  help.

**Tested 2026-10-21, 21:13Z, in a third session** (profile default; it never
ran `/login`, and its credential was hot-swapped several times that day):

- With `~/.claude.json`'s `oauthAccount` at lab3@acme.xyz (another session
  had just run `/login` as it) and the live token AI-Eng-3's, Claude in Chrome
  worked in this session. Two browsers were connected to the account, and
  the one picked last was used. So a running session does not keep the
  identity it started with: the "in memory" guess is wrong in that form.
- `cs use work-team`, then a browser call: it still worked, so a switch
  does not break a bridge connection already open. Switched back to
  AI-Eng-3 afterwards.
- Likely, still to confirm: the check that fails ("the OAuth token Claude
  Code is using belongs to a different claude.ai account than the one
  Claude Code is logged in as") runs when Claude Code connects to the
  bridge, and compares the live token's account with `oauthAccount`, which
  a hot swap leaves alone (`cs whoami` already notes the cached account
  block does not follow a swap). A connect after a swap would then fail
  until `/login` brings `oauthAccount` in line. To confirm: swap, then
  `/chrome` to force a reconnect (only a person can type it).

### Proposed answers (2026-10-21, not decided)

What claudeswitch can and cannot do, given C1: it cannot read or write the
extension's login, and an extension signs in only through claude.ai's own
approval page, which a person clicks.

- **P1. Keep Claude Code's recorded login in step with each swap.** On every
  swap (`use`, the daemon, a seed), also rewrite `oauthAccount` in that
  profile's `.claude.json` to the new account, from what the vault already
  records (email, account uuid, organization). It touches that one key, with
  a read-modify-write. This is the likely cause of the bridge refusal above,
  and Claude Code's "logged in as" would stop naming an old account too. To
  test before building: does a running Claude Code keep `oauthAccount` in
  memory and write it back over the change?
- **P2. "Rotate the Chrome login": one Chrome profile per account.** The
  extension's login inside one Chrome profile cannot be moved, but a Chrome
  profile per account, each signed in once, needs no move. The bridge picks
  the browser signed in as the live account by itself (GROUND_TRUTH §45),
  and with one browser per account the "several browsers, none selected"
  prompt never comes up. `cs chrome add <account>` already does this. The
  proposal: make it the recommended setup and have `setup` and the app offer
  it per account, rather than one shared Chrome profile per profile.
  Remembering a named browser per account (its display name and deviceId,
  from ai-studio-9d) only helps a session pick one, through its own
  `select_browser`; claudeswitch cannot call that tool for it.
- **P3. Show the mismatch.** The status line, `cs status` and `cs doctor`
  read `oauthAccount` (not secret) and mark it when it is not the live
  account, e.g. `◎ AI-Eng-3 29% · login lab3`, or `login personal ≠` when
  it differs. `doctor` says which command lines them up.
- **P4. Hold a rotation while a browser task runs.** The transcript detector
  already sees each session's recent tool calls; a `claude-in-chrome` call
  in the last few minutes would make the session busy, as a mid-turn session
  is, so rotation waits for a gap (and still goes at the hard floor).
- **P5. Record identity changes.** The audit and `cs why` note when a swap
  left the recorded login on another account.
- **P6. A home account for claude.ai objects: warn only.** Where an Artifact
  is published is Claude Code's choice, made at the time, so claudeswitch
  could only show the live account's organization (P3).

**Decided 2026-10-21 (owner):** build P1 (keep the recorded login in step
with each swap, after testing that a running Claude Code does not write its
own copy back), P3 and P5 (show and record the mismatch), P2 (a Chrome
profile per account as the recommended setup) and P4 (hold a rotation while
a browser task runs), with P4 a setting that can be turned off. P6 is not
being built.

**Decided 2026-10-21 (owner), for 0.7.8:** P4 is on by default, holding for
5 minutes after a session's last `claude-in-chrome` call (`0` turns it off).
P1 writes the whole `oauthAccount` block from one profile read per swap; if
the read fails, it writes the identity fields it already knows and fills in
the rest on the next poll.

**Built for 0.7.8 (2026-10-21):** P1 (`internal/cclogin`; every swap
rewrites the recorded login from a profile read; the daemon repairs a stray
or partial one; `cs whoami --sync`), P3 (status line `· login <name> ≠`,
`cs doctor`), and P5 (`login` audit events, `cs why`). The field mapping was
checked against a real answer, GROUND_TRUTH §53.
P2 (setup's offer, the notice's and the banner's "its own Chrome profile",
the Chrome guide's recommendation) is built for 0.7.8 too.
P4 is built for 0.7.8 too (`hold_for_browser`, default 5m, 0 off). Owner
decision, 2026-10-21: only the hard floor ends a hold early; max_switch_wait
does not. A switch away from a refused account is not held.

## Plan: claude.ai work across rotations (proposed 2026-10-21, not decided)

The 0.7.8 test (GROUND_TRUTH §55) fixed what it set out to: Claude in Chrome
connects after every switch. Deleting its pages showed four problems it had
hidden:

1. **Late pickup.** For 10–60 s after a swap, Claude Code's claude.ai tools
   (Artifact; likely Docs and Chrome too) still act as the previous account.
2. **Scattering.** Each artifact belongs to the account live when it was
   published. Listing, updating and deleting it work only while that account
   is live.
3. **Chrome shows one account.** An artifact opens in Chrome only when
   Chrome's claude.ai is signed in as its owner.
4. **Unexplained.** Another session's Claude in Chrome stayed "not connected"
   after `/chrome` while this session's worked.

claudeswitch cannot make a running Claude Code take up a swap at once, and
cannot change an artifact's owner or sharing. It can control *when* a
session acts and *which* account is live when it does.

**Phase 0: measure and diagnose (no code).**
- 0a. Measure the delay: switch three times, and after each list artifacts
  every few seconds until the answer changes account. That gives the wait
  for S below. Each switch moves every session in the profile for a minute.
- 0b. Diagnose problem 4 with the session that has it: its connected
  browsers, `cs doctor`, its Claude Code version.

**Phase 1: make claude.ai work safe under rotation (plugin hooks).**
- **S. Settle.** Before an Artifact, Docs or Claude in Chrome call, if the
  session's profile swapped less than the measured delay ago, the hook waits
  out the rest, then lets the call go. Nothing goes out as the previous
  account.
- **G. Owner guard.** After a publish, claudeswitch records which account owns
  the URL. Before a call on that URL (republish, read, comments, data,
  delete) while another account is live, the hook refuses, with a reason
  Claude sees: whose it is, and `cs use <owner>` if that account has room.
  Or it switches back by itself (a decision).
- **H. Hold for claude.ai work.** P4's hold extends to Artifact and Docs
  calls: a rotation waits 5 minutes after the last one, as after a browser
  call, and still goes at the hard floor.

**Phase 2: a home account for claude.ai work (setup and docs).**
- **P.** A project whose work lives on claude.ai (Product Oracle: artifacts,
  Chrome walks of each deploy) runs in its own profile, pinned to one home
  account. Rotation goes on in the other profiles. With Chrome's claude.ai
  signed in as that account, its artifacts open and its bridge matches.

**Phase 3: optional, a browser per account.**
- **C.** Each account has its own Chrome profile, signed in to claude.ai and
  to Claude in Chrome (P2). claudeswitch remembers each browser's device id,
  and after a swap the hook tells Claude to select the live account's
  browser before its next browser call. Most moving parts of all of these.

**From the Claude Code docs (checked 2026-10-21), which change the plan:**
- Hooks can carry S and G. A PreToolUse command hook may wait (default
  timeout 600 s; on a timeout the call goes ahead unless the hook sets
  `onFailure: "block"`). It can refuse a call with a reason Claude sees
  (`permissionDecision: "deny"`), add context, or rewrite the input.
  PostToolUse hooks get the tool's result. The Artifact tools' names and their
  result's shape are not documented, so a logging hook reads them first (0c).
- There is no supported way to make a running session switch subscription
  accounts other than `/login`. `CLAUDE_CODE_OAUTH_TOKEN` is read at start only
  and turns Chrome off, and apiKeyHelper is an API-key path that bypasses
  subscription limits. Making the swap take effect at once is out.
- Claude in Chrome needs the extension "signed in to the same organization as
  Claude Code"; otherwise its tools return "Browser extension is not connected"
  (docs: Chrome). That is problem 4's message. Hypothesis: a running session
  keeps the organization it started (or last `/login`ed) with. The Oracle
  session resumed when its recorded login may have named another organization.
  This session started on Acme, which would explain why its bridge survived
  even the switch to `personal`. Test: `/status` in that session (0b); if it
  names another organization, restarting the session (`claude --resume`) picks
  up the recorded login P1 now keeps right.
- **R. Rotate browser sessions within their organization** (new): while P4's
  hold applies, or as a setting, a profile whose sessions use Claude in Chrome
  rotates only to accounts in the same organization. With five Acme seats in
  the team pool, rotation can go on without ever breaking the bridge.

**Decided 2026-10-21 (owner):** run phase 0 in full; build S, G, R and H in
phase 1; G refuses with instructions (whose artifact it is, and `cs use
<owner>` when that account has room) rather than switching back by itself or
only warning. P (a pinned profile) is not being set up; C (a browser per
account) waits until after phase 1.

**Decided 2026-10-21 (owner), phase 1 details:** H is one setting renamed to
`hold_for_claude_ai` (Claude in Chrome, Artifact and Docs calls, default 5m;
`hold_for_browser` is still read as its old name). R: when no account in the
live account's organization has room at the hard floor during a hold, the
forced switch goes to the best account in another organization and says Claude
in Chrome may need `/chrome`. S is a setting, `settle_after_swap`, default 45s
(0 off).

**Phase 1 shipped in 0.8.0 (released 2026-10-22):**
- **H.** `hold_for_claude_ai` (default 5m) replaces `hold_for_browser`, which
  is still read from a file and still taken by `cs config get`/`set`; a set
  rewrites the line. The detector counts `Artifact`, `ArtifactComments`,
  `ArtifactData` and `mcp__claude_ai_Claude_Docs__*` calls beside Claude in
  Chrome ones; claude.ai's other connectors do not count. The hold reads
  `session busy (claude.ai work 2m ago)`; state.json keeps the last call as
  `claude_ai_at`.
- **R.** A switch that goes during a hold (forced, or off a refused account)
  takes the best account in the live account's organization, short of the
  landing margin if need be; with none there, the best elsewhere, and its
  reason says `Claude in Chrome may need /chrome`. An account's organization
  is its `org_id`, or else its last reading's. A switch the hold delays is not
  narrowed.
- **S.** `settle_after_swap` (default 45s, 0 off, at most 5m). The plugin's
  `PreToolUse` hook (`claudeswitch hook pre`, timeout 330 s) waits until that
  long after the profile's last switch, again for a switch while it waits, and
  never answers a permission.
- **G.** The plugin's `PostToolUse` hook on `Artifact` (`hook post`) records
  each publish's owner (id, file, session, account and seat) in
  `artifacts.json`, unless a switch while it ran makes the owner uncertain.
  `hook pre` denies an Artifact, ArtifactComments or ArtifactData call on a
  recorded artifact while another account is live, saying whose it is and
  either `cs use <owner>` (when it has room) or publishing a new one. `open`
  is never refused; a file path counts only in the session that published it.

## Plan: a browser per account (phase 3, proposed 2026-10-22, not decided)

Two things still follow the browser, not the live account, after 0.8.0:

1. **Which browser a Chrome task reaches.** Claude Code meets the extension on
   a bridge channel for its live account (C1, GROUND_TRUTH §45), and an open
   connection survives a switch (tested 2026-10-21 21:13Z). What a *new*
   connection after a switch finds is not settled: the 2026-10-07 reading
   was a channel per account uuid; the Chrome docs say "same organization";
   and in §55 one browser served all three organizations, possibly because
   the connection never reopened.
2. **Which artifacts open in it.** claude.ai shows an artifact only to its
   owner (§55): "This artifact is private" in the same organization, "Page
   not found" in another. What decides it is the browser's own claude.ai
   sign-in, not the extension's.

Today all eight accounts map to one Chrome profile ("Claude-In-Chrome", set
2026-10-21), so every Chrome task and every artifact view goes through one
claude.ai account. The accounts are in four organizations, five of them in
Acme's team organization.

**C0. Measure first (needs the owner's hands).** Sign a second Chrome
profile in to claude.ai and the extension as another account in the same
organization, and a third as an account in another organization. Then:
- in a new session after each `cs use`, `list_connected_browsers`: is the
  channel per account or per organization?
- in an open session, a browser call 0, 15, 30 and 60 s after a switch:
  does it move to the new account's browser, and when? (S already waits 45 s
  before each browser call.)
- with a profile's windows closed, does its browser still connect?

**C1. Refuse opening another account's artifact in Chrome** (any shape
below). G's book already knows each artifact's owner. `hook pre` on
`mcp__claude-in-chrome__navigate` (and `tabs_create_mcp` with a URL) to a
recorded artifact owned by an account that is not live would refuse, as G
does, with `cs use <owner>`, rather than let the page load as "private".

**C2. Remember each account's browser.** `hook post` on
`list_connected_browsers` and `select_browser` records the device id and
display name in use while each account was live. When a browser tool fails
with several browsers connected and none selected, the chrome hint names
the one claudeswitch saw that account use, so Claude's question to the
person lists it first. claudeswitch never selects a browser itself (the
tool requires the person to choose).

**C3. The setup, by C0's answer.**
- Per account: one Chrome profile per account (`cs chrome add`, P2), eight
  here, each signed in twice by hand and kept open. Chrome tasks and
  artifact views follow every rotation.
- Per organization: four, one per organization. Chrome tasks connect after
  any switch inside it, which R already keeps to during a hold; artifacts
  open only for each browser's own account.
- One shared browser (today): nothing to set up; C1 says why an artifact
  will not open and how to get there.
`cs doctor` would say which accounts have no browser of their own, and
what that costs them.

**Decided 2026-10-22 (owner):** start with C0 (measure, with a same-organization
and an other-organization account each in a Chrome profile of its own); build C1,
refusing as G does; decide C2 once C0 has answered whether several browsers
share a channel.

**C1 built (2026-10-22), for the next release:** `hook pre` refuses a
`navigate` or `browser_batch` navigate to another account's recorded artifact
when the live account has a Chrome profile of its own (mapped to no other
account). A shared Chrome profile refuses nothing, since whose claude.ai it
shows is unknown; on this Mac all eight accounts share one today, so C1 only
acts once C0's per-account profiles exist.

**Decided 2026-10-22 (owner), after C0 (GROUND_TRUTH §56: a new connection
needs a browser on the same account, not the same organization):**
- **R re-aimed.** During a hold, a switch that goes anyway prefers an account
  with a Chrome profile of its own (mapped to it alone), where a new Claude in
  Chrome connection finds a browser; otherwise the best account anywhere, and
  its reason says a new connection needs a browser signed in as the target.
  The organization no longer narrows anything.
- **C3:** the team pool's five accounts (work-team, AI-Eng-1, AI-Eng-2,
  AI-Eng-3, personal) get Chrome profiles of their own; the other three
  keep the shared one.
- **C2** is not being built.

**Shipped in 0.8.1 (released 2026-10-22):** R re-aimed (a switch during a hold
prefers an account with a Chrome profile of its own) and C1. Setup the same day:
personal, work-main and work-team no longer map to
"Claude-In-Chrome", leaving it AI-Eng-3's own; work-team's Chrome profile
(`claudeswitch-work-team`) opened for its sign-ins, with AI-Eng-1, AI-Eng-2 and
personal to follow.

**C3 done for seven of eight (2026-10-22/23).** Each account has a Chrome profile
of its own whose extension is on its channel and whose claude.ai opens its own
artifact: work-team ("Claude-Acme-Team", device 33618a63), AI-Eng-1 ("AI-ENG-1",
9b913557), AI-Eng-2 ("AI-ENG-2", 677e7ea4), AI-Eng-3 ("AI-ENG-3", a507c1db),
personal ("Claude-Acme-Personal", 9d485bc2), personal ("Claude-Personal",
e2cce59e), work-team ("Claude-Acme-Devops-Team", 2ff3b1e1). Open:
work-main ("Claude-Acme-Devops-Main", 067c70fc), whose extension keeps
signing in to alice@example.com's own organization, work-team's seat.

**C4. Claude in Chrome follows the account (owner request and decisions,
2026-10-23).** The owner: Claude in Chrome should use the Chrome profile set for
the account live in cs, switch to it automatically, and never ask him to sign in
again. A new bridge connection finds only a browser on the live account's own
channel, and a profile's extension connects only while the profile runs;
launching one brought its browser back in 3 s (GROUND_TRUTH §56). Decided:
claudeswitch opens the new account's own Chrome profile right after every switch
(the daemon's, `cs use`'s and the app's, never in a dry run), in the background
(`open -g`). Built the same day:
- the switch line says `Claude in Chrome: opened <account>'s Chrome profile
  "<name>"`; `use --json` carries it as `chrome`;
- no notice asks for a sign-in: a shared profile's chrome says the account
  needs a Chrome profile of its own (`cs chrome add`), and the app's banner
  offers that instead of "Sign in as";
- `chrome hint` (after a failed Claude in Chrome call) opens the live account's
  own profile itself, once per rotation, and tells Claude to retry, never to ask
  the person to sign in, and that a session started on another account reaches
  the live account's browser through a new session.
Not in claudeswitch's reach: a running session that lost its connection does not
move its bridge to the new account even after `/chrome`; a new session does.

**Shipped in 0.8.2 (released 2026-10-23):** C4. Then, the same day:
- **work-main** keeps no browser of its own (owner decision): its
  extension stays bound to alice@example.com's own organization, as a user report
  describes the extension staying bound to whichever account first opened it
  (anthropics/claude-code issue 82455, closed as not planned).
- **A running session follows with `/mcp` Reconnect** (GROUND_TRUTH §56), so
  `chrome hint` now names it before a new session.
- The `cs use` refusals at 11:51 ("could not be confirmed absent") were the work
  profile's live token having expired (R2: no identity call with an expired
  token); the daemon renewed it at 11:54 and the refusals stopped.
- AI-Eng-3's channel also had BGD's main Chrome profile ("1-Alice Personal",
  device dbb0f289), whose extension was signed in as AI-Eng-3; BGD signed it out
  (2026-10-23 14:58), leaving one browser per account: work-team, AI-Eng-1,
  AI-Eng-2, AI-Eng-3, personal, personal and work-team each have a
  Chrome profile of their own and no other browser on their channel.

**Lessons of 2026-10-23 (owner: "learn from this last set of interactions and
apply fixes").** A deck session (newdeck-85) needed Claude in Chrome in the
profile where Google is alice@example.com, and lost it twice: a test `cs use`
from the claudeswitch session moved the default profile under it, and a
`cs use` went over the pin the owner had set. Its own running session never
reconnected, while fresh `claude -p --chrome` processes always reached the
live account's browser; it finished that way. Built (owner decisions the same
day):
- **A pin holds.** `cs use` of another account in a pinned profile refuses
  (`pinned`); the daemon puts a pinned profile found elsewhere back on its pin.
- **A busy guard on manual switches.** `cs use` refuses (`claude_ai_busy`)
  while a session in the profile did claude.ai work within
  `hold_for_claude_ai`; `--force` overrides, and the app retries with it, as
  its switch is the owner's click.
- **A no-op `use` is no switch**: it answers `already` and leaves the switch
  time, so no session waits out a settle for nothing.
- **`cs chrome check`** asks a fresh process which browsers the live account's
  channel has, opening the account's own Chrome profile if none answers.
- **The hint and the `/cs switch` skill** name the fresh-process route, and
  the skill tells Claude never to switch a profile other sessions work in on
  its own initiative, nor to unpin or `--force` without the user.
- **`cs chrome add`** warns, for an email with seats in several
  organizations, to switch claude.ai to the account's organization before the
  extension is first opened (it stays bound to the first one).
