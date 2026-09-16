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

### 4.4 Unknown is not "fine"

An account whose usage cannot be read is `unknown`, and `unknown` is never a rotation
target. Symmetrically, an *unknown active* account causes a hold rather than a rotation —
rotating away from a working session on no evidence is worse than waiting for the poller.

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
