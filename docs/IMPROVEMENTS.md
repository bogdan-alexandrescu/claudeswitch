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
  "Move to X", for listed and D6 accounts.
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
  it joins. The suggested name follows the source.
- **M9. A card's second button is "Switch to best"** (the account with most
  room in that profile); the account picker stays for choosing a specific one.
- **M10. Accounts rows get "Move to profile"**, with the same confirmation and
  checks as the Profiles pane; one verb, "Move to X", for every move.

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