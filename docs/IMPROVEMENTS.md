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
  notarization ($99/yr) can be added later without changing the app. Not the
  App Store: sandboxing would forbid running the binary and reading its state.

## Later lanes — experience

- **I5. Re-measure the usage-endpoint limit, then redesign the cadence.** The
  daemon log shows daily 429s (243 on 09-28, 323 on 10-01, ~25/day since) and
  stale-reading decisions. claude-swap measured ~30 calls per trailing hour per
  identity, against our 20 s hot polling (~180/hour). The probe deliberately
  trips the limit (up to ~1 h blind on the probed account) and must be
  scheduled. Then: per-account hourly budget, movement-driven cadence, backoff
  after a 429, no 20 s hot polling. Touches DESIGN 4.3/4.3b, GROUND_TRUTH §34,
  PROFILES D3/D15/D16.
- **I6. Per-model weekly limits.** Parse `limits[]` entries of kind
  `weekly_scoped` with `scope.model`; show them in `status` and the status
  line; optional policy input. Capture a real response first; an unknown scope
  is unknown, never fine. Fix the contract test to watch `limits[]`.
- **I7. `profile create` / `run`.** One command makes a profile (dir, shared
  settings/skills symlinked, user MCP servers mirrored, a credential seeded for
  Claude Code to adopt, `[[profile]]` written); `run` launches `claude` in it.
  Must keep §3: the seeded account is one live nowhere else.
- **I8. Weekly pace view.** Expected vs actual weekly %, and "~X% will expire
  unused at reset". Display only — no change to room-first selection.

Not chosen for now: argv fallback for oversized writes, a use-it-or-lose-it
selection strategy, `oauthAccount` splicing, `service install` from the binary,
`schemaVersion` on JSON, session records as the busy signal.
