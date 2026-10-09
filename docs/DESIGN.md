# claudeswitch — design

Companion to `docs/GROUND_TRUTH.md` (what is true about the system we sit on) and
`BUILD_PROMPT.md` (what we set out to build). This file records *why* the program is
shaped the way it is, what will break it, and what would prove it wrong.

Verified against `claude 2.1.266` on macOS 15.2 (Darwin 25.2.0), 2026-09-08/09.

---

## 1. The problem, stated precisely

Four Claude accounts exist. One is in use. When its quota runs out, work stops — for up
to a week, if it is the seven-day ceiling that went. The other three sit idle throughout.

The daemon's job is to notice early and move the live credential to an account that still
has room, without the user losing their session or their MCP logins.

## 2. How the design changed, and what changed it

Worth recording because two conclusions were wrong on the way, and the corrections are
what the current shape is built on.

**First position: predictive, from token counts.** Sum the `usage` blocks in transcripts,
learn each account's ceiling from past rejections, rotate at 15% remaining.

**Killed by measurement.** Four weightings tested against 24 real limit hits over 21 days.
Best spread was CV 40%, worst 79% — nowhere near enough to drive a percentage trigger.
Recorded in ground truth Round 2. This is why the code contains no token estimator.

**Second position: purely reactive.** Rejections are written locally as `<synthetic>`
transcript records within milliseconds, carrying the exact window and `resets_at`. Since
the credential hot-swaps under a live session, reacting costs one refused request.

**Superseded by a better fact.** `/usage` inside Claude Code is backed by
`GET /api/oauth/usage`, which returns exact per-window utilization — and authenticates
with *whatever token you present*, so every vaulted account can be polled without
switching to it. That dissolved the hardest problem in the original design (you cannot
measure an account you are not using) and restored prediction on solid ground.

**Current position: predictive, with the reactive path kept as a safety net.** Poll the
truth; catch what falls between polls.

The lesson worth keeping: the first two positions were each defensible from the evidence
available at the time. What settled it was going and looking, twice.

## 3. Architecture

```
usage poller ─────┐
                  ├──> account state ──> policy engine ──> switch executor ──> vault
rejection detector┘         (durable)      (pure fn)         (verified)      (keychain)
                                │                                  │
                                └────────── audit log ─────────────┘
```

- **Usage poller** — primary sensor. Exact utilization per account per window.
- **Rejection detector** — safety net. Authoritative `resets_at`, no network, ~ms latency.
- **Account state** — durable; the seven-day window outlives restarts.
- **Policy engine** — pure function of (config, state, clock). Every rotation is
  reproducible from the audit log.
- **Switch executor / vault** — merge-not-replace, verified by org id, rollback verified.

## 4. Decisions and their reasons

### 4.1 Only `claudeAiOauth` is ever moved

The live Keychain item holds **both** the Claude credential and `mcpOAuth` — the user's
Notion and Slack tokens. Replacing the blob wholesale would sign them out of every MCP
server on every rotation. `MergeForSwap` takes `claudeAiOauth` from the vault and carries
`mcpOAuth` over from whatever is live. Vault entries never contain `mcpOAuth` at all:
MCP logins belong to the machine, not to an account.

This is the single most important function in the program and has four dedicated tests,
including one asserting the merged blob does not alias the vault's record.

### 4.2 Secrets go over stdin, never argv

`security add-generic-password -w <secret>` puts the token in `ps` output for every
process on the machine. Writes go through `security -i` with the command on stdin instead.
Every write is verified by reading it back; a mismatch is an error, not an assumption.

### 4.3 The call budget is 4, not 5

Measured: ~5 calls buys a 299-second lockout, and there are no rate-limit headers on a
success — the budget can only be discovered by exhausting it. So it is held by
construction: a token bucket of 4 per 5 minutes, with one slot reserved for the
pre-switch verification, which is the call that actually matters.

Scheduling exploits one fact: **an idle account's utilization can only go down.** Nothing
is spending it. So the active account is polled every 60–90s (tighter near the trigger),
inactive accounts every 10–15 minutes staggered, and a switch target immediately before
moving to it.

### 4.3a Spend the budget only on calls that can happen

The call budget is charged when a request is actually possible, not when one is
contemplated. Charging first meant placeholder accounts with no credential silently ate
two thirds of the allowance (2026-09-09). Resolve the credential, then spend.

### 4.3b One budget for the machine

The usage endpoint's limit applies to the whole machine. A budget owned by the poller,
which the vault could walk around, is not a budget — and that is exactly what happened:
five unbudgeted vault calls plus the poller's four per five minutes produced a 429 storm
that starved the poller and corrupted the daemon's idea of which account was live.
`usage.Shared()` is the single instance every caller must use, and a 429 always yields at
least a 60-second backoff however small the `Retry-After` header is.

### 4.3c Each account has its own allowance, and the cadence follows movement

(2026-10-07, IMPROVEMENTS I5.) The endpoint's limit is per **account**
(GROUND_TRUTH §42): about 24 calls at 20-second spacing, then refused for 10–15
minutes with a meaningless `Retry-After: 0`, while other accounts are answered
normally. The machine-wide window of 4.3b cannot see that — one account read
every minute is 5 calls per 5 minutes, well inside it, and still empties that
account. So the budget holds a second allowance per credential, in the same
shared ledger, keyed by the same digest as the 429 locks:

| | value | why |
|---|---|---|
| burst | 20 calls | below the measured 24; the difference is room for Claude Code's own calls on the live account (§40), which we cannot see |
| refill | 1 call per 2 min (~30/h) | the fastest refill that, with a burst of 20, still runs dry by the 24th call at 20 s — any faster predicts calls the endpoint refused; it also matches claude-swap's ~30/h |
| swap reserve | 3 calls | only a swap check (verifying the incoming account, re-reading the live one) may spend them, down to the last call |
| hot reserve | 10 calls (`hot_reserve`, 0–15) | routine polls stop above it, so it rebuilds between spells (at the 3m default, 20 reads an hour against a live refill of ~28: ~8 an hour); a hot poll may spend it — a hot spell needs 7.5 — and so may an *overdue* read of the account in use (reading older than `poll_active` + 1/12, 3m15s at the default), down to 1 above the swap reserve, so after a spell the reading stays under the daemon's 4m stale-decision cap. In the day simulation a second full spell 56 minutes after the first began with the reserve full and kept its readings within 55 s |
| unseen spend | 2 calls/h (`unseen_calls_per_hour`, 0–20) | a credential some profile is running on (marked live in the ledger whenever its item is read) is modelled as refilling 2/h slower, for Claude Code's own reads of the endpoint (§40, rate not measured) |

Burst, refill and swap reserve are constants (measured or structural); the hot
reserve and the unseen spend are advanced settings (owner decision 2026-10-07),
global only, shown on `doctor`'s account-rate row.

A poll that would dip into its tier's reserve is **deferred**, not made: the
poller reschedules that account for when its allowance has refilled and gives
the tick to the next due account. Interactive calls are charged and never
refused (4.3b's deadlock still applies).

What the numbers mean in practice: polling an idle account every 2 minutes or
slower never drains it. `poll_active = 3m` (the default, owner decision
2026-10-07, superseding 2m) is 20/h on the account in use against a live refill
of ~28/h, so the hot reserve rebuilds about 8 calls an hour after a spell and
routine reads are not deferred. A hot spell at 60 s for the 15-minute lookahead
spends 15 and regains 7.5, net 7.5 of the 10 held for it. 20-second polling
runs dry in ~8 minutes, as observed.

The daemon warns "deciding on a stale reading" past `min(3 × poll_active, 4m)`
(4m at the default), so routine operation never warns: a read at 3m plus its
tick, or an overdue read at 3m15s after a spell, stays under it. The watchdog's
blind-exit limit is `min(10 × poll_active, 10m)` (floor 2m); blind failover
(4.4) takes `blind_failover_polls` unreadable polls and a last good reading at
least that many `poll_active` old: 3 × 3m = 9 minutes at the defaults.

A `poll_active` or `poll_idle` under 2 minutes is **not refused** (owner
decision 2026-10-07): every `cs config` on 0.3.x–0.5.0 wrote `poll_active =
"1m0s"`, because `Write` pinned the defaults it had been filled with, and
refusing that file would stop the daemon and the `cs config` that could fix it.
It loads at 2 minutes, `status` and `doctor` warn with the fix (`cs config
poll_active 3m`), and `cs config` refuses to set such a value anew. `Write` now
writes a setting only when the file carried it, `cs config` set it, or it
differs from the default; every other one is a commented default. A
`poll_active` of exactly `1m0s` — the value 0.3.x–0.5.0 `cs config` pinned — is
dropped by the next write, so the file returns to the 3m default; any other
`poll_active` or `poll_idle` under 2m is the person's own, written back as they
wrote it, and keeps loading at 2m with the warning. A fast `poll_hot`
stays a warning, as decided earlier, since the allowance defers its excess.
`doctor` prints the per-account rates.

**Hot polling is movement-driven.** The account in use is read every
`poll_hot` only while it is *moving* — its reading rose recently (within the
time 1.5 points take at its burn rate, at least two hot polls and at most 5
minutes) at ≥ 0.1 points/min — **and** at that rate it reaches its trigger
within the 15-minute lookahead (`hot_threshold` stays a floor, crossed early only
by a burn of 1.5 points/min). Sitting still at 90% is not hot: nothing is
changing, and a faster reading would say the same thing. Leaving hot, the
interval doubles back toward `poll_active` rather than jumping. D3/D16 still
gate hot polling to a busy profile when there are several. A re-attribution
read of the live account counts as its poll, and an account that becomes the
one in use is read once its reading is `poll_active` old rather than waiting out
an idle schedule (in a busy profile; a quiet one's waits `poll_idle`, below).

**A quiet profile's live account is read at `poll_idle`** (R4, owner
2026-10-09). "Quiet" is D15's test: no transcript write in the profile within
`poll_active`. Nothing is spending that account from this machine, so a
reading `poll_idle` old says what a fresher one would; it is not pulled in as
overdue at `poll_active`, never spends the hot reserve, and the daemon's
stale-decision warning allows it `poll_idle` more. The moment the profile is
busy again its account is overdue and read on the next tick. With one profile
an account still polls hot while it moves within reach (D16). The poller
reports a profile as unknown (not quiet) when no busy test is wired, as in
tests.

**A restart starts each schedule from the last reading** (R4). An account with
no schedule yet is due at its `LastAt` plus its routine interval, not at once,
and a profile whose live token is the one last attributed (`live_key`),
unexpired, with its account read within `poll_active`, skips the startup read
altogether (`Poller.ResumeIn`). A restart used to read every profile and
every account in its first ticks.

**An expired access token is never sent** (R2, GROUND_TRUTH §46). The usage
endpoint answers one with 429, not 401, so each call was a strike and no
backoff ended them. The poller, the vault's seat probe and the swap's verify
check the credential's own expiry first: past it, no call, no strike, no
failure counted toward blind failover, and `last_error` says "access token
expired; parked until it is renewed". A swap installing an expired token
stands unverified — Claude Code renews it on first use. A parked account is
looked at again every minute (a local read, no call).

What unparks it: a busy profile's session renews its own token. A quiet
profile's live token, once expired or within `refresh_window`, is renewed by
the daemon on its two-minute vault tick — `RefreshIn` on the profile's own
item, holding Claude Code's credential locks (§43), then a fresh read — at
most once per profile every 10 minutes, audited as `kind: refresh` with the
profile and account. Never a busy profile's: the refresh revokes the token its
session holds (§16). A session open but not busy loses its token; the owner
accepted that cost, and the keychain write. Not when `auto_refresh` is off,
and not for a live account with no vault entry (`RefreshIn` renews through the
vault). A dead refresh token writes the needs-login error into the account's
`last_error`, where `status` and the app's sign-in banner read it, and sends
one notification.

**After a 429** the account backs off on our own schedule: 5 minutes, doubling,
at most 20 (at most 10 for the account in use, since 5 + 10 covers the measured
recovery). "In use" is any credential marked live in the ledger in the last hour,
so the cap holds whoever is refused — the vault's swap and probe calls
included. A **long** `Retry-After` (over 5 minutes) is the server saying the
refusal will last (R3, owner 2026-10-09): it is honoured up to a step that
grows with each long-wait refusal of the same token since its last success —
20, 40, then 60 minutes for an idle account; 20, then 30 for the account in
use — and never cut below what the step allows. `Retry-After: 0`, §42's burst
limit, keeps the 5-minute first strike. `MaxLock` is the longest step, 60
minutes: the watchdog no longer counts a lock as blindness, so it no longer
bounds it. Every 429 logs the header (`retry_after`) beside the backoff
applied. A 429 never counts toward blind failover (4.4), and the transcript
detector still re-decides at once on a refusal, whatever any lock says.

`internal/poller/simulation_test.go` replays a working day (two trigger
crossings on one account, three idle accounts, Claude Code's own reads, a
transcript refusal) against a fake endpoint enforcing §42 on a fake clock, and
asserts zero 429s and readings no older than `poll_hot` while hot and moving.

### 4.4 Unknown is not "fine"

An account whose usage cannot be read is `unknown`, and `unknown` is never a rotation
target. Symmetrically, an *unknown active* account causes a hold rather than a rotation —
rotating away from a working session on no evidence is worse than waiting for the poller.

**The hold has a limit** (amended 2026-10-07, IMPROVEMENTS A2). Waiting for the poller is
right while the poller is about to catch up; it stops being right once it plainly is not.
A daemon blind on the account being spent cannot see it approach its limit, and the
stale reading's projection is only a guess. So when the active account's usage has been
unreadable for `blind_failover_polls` consecutive polls (default 3), and that streak has
itself lasted at least that many `poll_active` intervals (counted from
`FailSince`, the first counted failure), the policy fails over to a *healthy* account:
one whose own last poll succeeded, under its triggers and clear of the landing margin
(4.4a). It respects the cooldown, and D18/§3 still refuses a target live in another
profile. It is marked `Failover` and is never forced: the swap waits for an idle gap
and `max_switch_wait` does not apply (a refusal still switches at once). With no
healthy account, it keeps holding (a Stay, not a Wait).

**It never splits a turn** (owner decision 2026-10-07). Other rotations wait for an idle
gap only up to `max_switch_wait` and then go ahead mid-turn; a blind failover waits for the
gap however long it takes. Nothing says the work is in trouble — only that we cannot see
the account — and a refusal, which does say so, still switches as before (4.6).

**The streak is measured from its first failure, not from the last good reading**, and
a daemon starts with no streak. After a long sleep the last reading is hours old while the
reads have been failing for seconds (the network is not up yet); measuring from the reading
would fail over a working session on wake. A streak inherited from an earlier process is
time this one did not watch.

Three things are deliberately not blindness:

- **A 429** from the usage endpoint is its own burst limit, per account, clearing within
  10–15 minutes (GROUND_TRUTH §42), and says nothing about whether the account can work.
  It neither counts nor clears the count. If the account really is refusing work, the
  rejection detector burns it (4.6) and that rotates on its own, blind or not.
- **An access token that expired on an idle session**, with a live refresh token. Claude
  Code refreshes it on the next message and the daemon re-captures it; failing over would
  move an idle session for nothing. The same expired token on a *busy* session is not
  explained that way — Claude Code would have refreshed it to keep working — so it counts.
- **A 401 on a stale copy.** The scheduled poll reads the active account through its
  vault entry, and Claude Code's own refresh revokes that token until the daemon
  re-captures it. When the profile's live item holds a different token, the 401 is about
  our copy, not the account, and it is not counted. Re-attribution polls through the live
  token itself, so a live token that really is rejected still counts.

`blind_failover_polls = 0` restores the unconditional hold.

### 4.4a A switch lands with room to spare

A rotation target's **session (5-hour) window** needs `landing_margin` points (default 10)
of room below `switch_at`, on the same figures eligibility uses (IMPROVEMENTS A1). Landing
one point under the line means the next poll rotates away again — or, with the cooldown,
holds the session on an account with no headroom.

The weekly window has **no margin** (owner decision 2026-10-07): any account under
`switch_at_weekly` is a valid target. The weekly trigger sits near 100 on purpose (a
weekly window at 92% still holds days of work), and a margin there would rule out exactly
the accounts worth landing on — an active account at 98.4% weekly should move to one at
92% now, not wait.

When every account with room is inside the margin:

- an ordinary rotation (over the trigger, under the hard floor) **holds** and says which
  accounts the margin excluded. It is a Stay, not a Wait: "every account is out" would be
  false, and the hard floor still guarantees the switch happens before it matters;
- past the **hard floor**, or after a **refusal**, staying is worse than landing close to
  a trigger, so it takes the best account inside the margin and says so;
- with no active account at all it does the same, since there is nothing to stay on.

`why` marks an account excluded by the margin and gives its session room; `plan` names
them in the reason. `landing_margin` and `blind_failover_polls` are written to the config
only once set, so an untouched install follows the defaults as they change.

### 4.5 The reserve governs entry, not tenancy

Personal is ineligible as work overflow above 70% utilization. But once personal is the
active account, the reserve does not evict it. Reserve answers "may I rotate *into* this?",
not "must I leave?". Easy to get backwards; has its own test.

### 4.6 A refusal outranks everything

A `rejection` record marks the account burnt until its `resets_at`, overriding any older
usage reading, and it bypasses the anti-flap cooldown. Cooldown exists to stop thrashing
between two marginal accounts; it must never keep someone sitting on an account that has
already said no.

### 4.7 The daemon never restarts Claude Code

Swapping the credential takes effect on a running session going forward. There is no
reason to signal, kill, or respawn anything, and the code does not.

### 4.7a Observe, do not remember — continuously

Where reality is observable, observe it, and keep observing it. Five bugs on 2026-09-09
shared this root cause: a stored belief was trusted over a fact that could have been
checked, or a fact was checked once and then remembered forever.

- `SyncActive` assumed a changed token meant a refresh, and overwrote a vaulted credential
  with a different account's. It now verifies the organization first.
- `state.Active` was treated as a preference the CLI owned, so the daemon's observation of
  which account was actually live could never correct it. It is now daemon-owned, because
  it is an observation.
- `refresh` asked `state.Active` whether an account was live, and with state cleared would
  have revoked the running session's token. It now compares against the live Keychain item,
  and when it cannot tell, it assumes the account IS live so that the caller is asked.
- `whoami` asked `claude auth status`, which reports Claude Code's cached account block —
  a cache that does not follow a credential swap. It reported the wrong account with full
  confidence. It now reads the organization from the credential itself.
- Attribution ran only at daemon startup, so one rate-limited call left the daemon
  believing the wrong account was active for its whole life. It re-derives continuously.

### 4.7b Distrust stored observations that cannot be attributed

Every state record carries the organization it was observed from, and
`State.Reconcile` — run on every load — discards any whose organization disagrees with the
`org_id` the config pins for that id, or that belongs to no configured account. A record
that cannot be shown to describe the right account is worse than no record, because the
policy engine will act on it: after an id was reused, an exhausted account read as having
70% headroom (2026-09-09).

Deliberate removals are remembered too (`State.Drop`), because the merge used to copy
every account back from disk and silently undo them.

### 4.7c Identity is the seat, not the organization

A quota pool is one person **within** one organization: `account.uuid@organization.uuid`
from `/api/oauth/profile`. Neither half identifies it. Two people in one organization have
separate quota — and may hold different subscriptions and plans. One person in two
organizations likewise: alice@example.com read 0%/46% in one and 23%/19% in another at the
same time. `anthropic-organization-id`, which the usage API hands back free on every
response, is the wrong granularity on its own.

Keying on the organization caused three faults: `SyncActive` overwrote one member's vaulted
credential with a colleague's, `add` refused legitimate accounts as duplicates, and two
seats of one organization would have been treated as one quota pool. Vault entries now
record the seat and the email; an entry with no recorded seat is refused, never assumed to
match; `claudeswitch identify` backfills older entries.

### 4.8a One account, one name — and the right one

Two guards, learned separately and both needed:

- **`add` refuses a credential whose organization is already vaulted under another name**,
  so running it without having actually switched accounts cannot create a healthy-looking
  duplicate (2026-09-09).
- **`add` refuses a credential whose organization does not match the account's configured
  `org_id`**, so a browser login that lands on the wrong account cannot file a stranger
  under a familiar name. Without this, an exhausted third account was stored as "personal"
  and nothing complained (2026-09-09).

`add` also prints the account email and organization name from `claude auth status`, not
just the uuid: the check is only useful if a person can perform it at a glance.

Vault entries carry a `claudeswitchMeta` annotation naming the organization they belong
to, and `add` refuses a credential whose org is already vaulted under another name. Without
it, running `add` without having actually switched accounts produces a healthy-looking
duplicate that the policy engine treats as a real rotation target. Observed on
2026-09-09. The annotation is stripped in `MergeForSwap`.

### 4.8 Attribution refuses to guess

A live credential is matched to a configured account by `org_id` (explicit in config, or
previously observed). If neither matches, it is reported as `(live) … unattributed` with
the org id printed for the user to pin.

The first implementation adopted "the first configured account without an org id" and
promptly filed a personal credential under `work-a`. Guessing an identity is worse than
admitting ignorance, especially when the guess decides which account gets spent.

### 4.9 The audit log records changes, not ticks

Evaluating every 20 seconds and logging "stay" each time produced 377 rows in one evening
and buried the single rejection that mattered. Decisions are recorded only when they
differ from the previous one; rejections, switches, severity transitions and errors always
are. An audit log that records everything records nothing.

### 4.10 Activity detection reads events, not the filesystem

Preferring an idle gap requires knowing whether a session is mid-turn. The first version
walked the transcript tree per tick — **7,654 files across 503 directories** — and showed
up as the daemon's entire CPU cost, pegged in `lstat`. The watcher already receives a
write event for every transcript change, so activity is now tracked from those, with one
walk at startup. CPU went from spinning to 0.0%.

### 4.11 Claude Code integration is a plugin over the CLI

Decided 2026-09-16. The integration with Claude Code is a plugin (`plugin/`,
listed by `.claude-plugin/marketplace.json` at the repo root) rather than
gstack-style symlinks into `~/.claude/skills`: installing and updating goes
through Claude Code's own mechanism, and claudeswitch does not write skills
into someone's config directory.

- **The plugin holds no logic.** Skills and the hook call the binary. What a
  reading means is decided in one place, and a plugin cannot disagree with it.
- **The SessionStart hook is read-only.** `claudeswitch context` reads
  state.json only. A hook that raised a keychain prompt on every session start
  would be worse than no hook. It always exits 0, and says so in one line when
  the binary is missing or too old.
- **Switching needs no confirmation; login does.** `use` is hot, verified and
  reversible. `login` writes a credential to the vault.
- **A plugin cannot set `statusLine`** (plugin settings only accept `agent` and
  `subagentStatusLine`), so the binary writes it: `statusline install`. It never
  replaces someone else's status line without `--force`, keeps a `.claudeswitch.bak`,
  and preserves key order in a hand-edited file.
- **The plugin is called `cs`, as the binary's short name is.** Claude Code
  already owns `/status`, `/login` and `/doctor`, so the skills need a
  namespace; `/cs status` (a router skill) reads the same as `cs status` in a
  terminal. v0.4.0 shipped it as `claudeswitch`; renamed in v0.4.1.
- **`/cs <command>` is the only slash form** (decided 2026-10-07). The
  per-command skills are `user-invocable: false`: they stay out of the slash
  menu, so `/cs:status` and `/cs status` no longer appear side by side, but
  Claude still picks them when asked in words, and the router still invokes
  them.
- **The plugin's version is the binary's.** `plugin/.claude-plugin/plugin.json`
  carries the release version and is bumped with every release; Claude Code only
  offers an update when it changes.

## 5. Failure modes, and what happens

| What breaks | What the daemon does |
|---|---|
| `/api/oauth/usage` changes shape | Predictive switching **disabled**, reactive-only, loud warning in `status`, a notification, and `doctor` explains it. Never falls back to guessing. |
| Usage API 429 | Expected. Honour `retry-after` exactly, show the reading as stale. Not a degradation. |
| Usage API unreachable for one account | That account is `unknown` → not a rotation target. |
| Keychain write fails mid-swap | Snapshot restored and the restore *verified*. If the rollback also fails, the error says so explicitly and tells the user to run `claude /login`. |
| Swap installs the wrong account | Caught by comparing `anthropic-organization-id`; rolled back. |
| Refresh token expired | Account refuses to be swapped to, and cannot be polled either. `doctor` and a notification warn before it happens. |
| Every account burnt | `wait`, naming which account recovers first and when. No thrashing. |
| Two daemons started | Second refuses on the flock. |
| CLI and daemon write state together | Ownership split — daemon owns observations, CLI owns active/pinned/last-switch — merged under a lock on every save. While a daemon runs, the CLI does not poll at all. |
| Corrupt state file | Reset with a warning. It is a cache of observations, all re-observable. |
| Invalid config | Load fails with an actionable message; a running daemon keeps its last good copy. |

## 6. Assumptions that would invalidate this design

Listed so that a future change can be checked against them cheaply.

1. **Credential hot-swap works.** If Claude Code ever caches the token for the process
   lifetime, the whole product becomes "queue a switch for the next session".
2. **`/api/oauth/usage` keeps returning per-window utilization** and keeps authenticating
   with any presented token. If it became scoped to the *live* session, inactive accounts
   would go dark and the design reverts to reactive-only.
3. **The Keychain item stays a single JSON blob** with `claudeAiOauth` separable from
   `mcpOAuth`.
4. **Rejections keep being written to transcripts** with `rateLimitType` and `resetsAt`.
5. **The rate budget stays around 5 per 5 minutes.** Materially tighter would force
   polling inactive accounts far less often.
6. **Accounts are the user's own and rotation respects each account's own limits.** The
   tool records every switch and every unit of usage against a named account precisely so
   this stays auditable.

## 6a. Account selection is not solvable from the CLI

Onboarding an account requires that account's credential to be live at some moment, and
**which account a login returns is decided entirely by the browser session**. Measured:
`claude auth login` returned the same organization four times running regardless of what
claude.ai displayed; `organization_uuid` on the authorize endpoint is accepted and ignored;
only `--sso` steers anything, and only toward the SSO-backed organization.

What claudeswitch can do is make the attempt harmless. `login --direct` runs the OAuth flow
itself — authorize, exchange, verify — and vaults the result only if the organization
matches what the config pins. The live credential is never installed, replaced, or touched,
whether the attempt succeeds or fails. So a wrong account costs nothing but a retry, which
is the most the tool can offer given the constraint above.

## 7. Known gaps

- ~~**Work accounts are unproven.**~~ Settled 2026-09-09: an SSO work account vaults
  cleanly and does receive a 27-day refresh token. Note its tier is `default_claude_max_5x`
  against the personal account's `max_20x`, and its email is identical — only the
  organization differs, which is why attribution keys on org id.
- **Severity may be a better trigger than our percentages.** The API's own ladder —
  `normal → warning (78%) → critical (90%) → refusal` — comes from the system that does
  the refusing. Transitions are being logged; the thresholds stay fixed until there is
  enough data to characterise the onsets.
- ~~**Token refresh is not implemented.**~~ **Implemented 2026-09-09.** Measured
  2026-09-09: refreshing *revokes* the previously issued access token, so a vaulted
  snapshot dies as soon as that account's session refreshes — hours, not the 27 days the
  refresh-token expiry suggests. See ground truth Round 6 for what the vault must do
  (refresh on a schedule with write-back; re-capture from live for the active account;
  detect drift between live and vault; treat a 401 on the pre-switch check as
  "needs re-login"). Endpoint: `POST https://platform.claude.com/v1/oauth/token`,
  `client_id 9d1c250a-e61b-44d9-88ed-5944d1962f5e`, both read out of the 2.1.266 binary.
  Built as `vault.Refresh` (persists the new pair BEFORE anything else, updates the live
  item too when the account is active, refuses the active account without explicit
  consent) and `vault.SyncActive` (re-captures the live credential when Claude Code has
  refreshed it underneath us — the safe half, needs no token endpoint, runs even in
  dry-run). **Proven 2026-09-09** against the SSO work account while the session was on
  another: token exchanged, refresh token rotated and persisted, new credential verified,
  live item and MCP tokens untouched.
- ~~**Detector latency is not instrumented.**~~ Now measured per rejection (transcript
  mtime → emit) and carried on the event; the worst value this run is tracked and logged.
  It is an upper bound: the record was written at or before that mtime.
- ~~**No contract test against a recorded usage response.**~~ `internal/usage/testdata/
  usage_response.json` holds a real 200 (2026-09-08, claude 2.1.266), asserted against in
  five contract tests: both windows carry utilization and `resets_at`, `resets_at` still
  parses, `limits[]` still carries `severity`/`is_active` and still includes `session` and
  `weekly_all`, `Binding()`/`Worst()` agree with it, and per-model windows are flagged if
  they ever start returning data. If it fails, re-capture and reassess — do not loosen it.
