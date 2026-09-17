# Ground truth — verified 2026-09-08

Machine: macOS (Darwin 25.2.0). `claude 2.1.266`, binary at `~/.local/bin/claude`.
Every claim below has the command output that produced it. Re-verify on any Claude Code
upgrade; these are all undocumented surfaces.

---

## 1. Credential store — CONFIRMED: macOS Keychain, single slot

```
$ security find-generic-password -s "Claude Code-credentials"
keychain: "~/Library/Keychains/login.keychain-db"
    "acct"<blob>="you"          # the unix username, NOT the Claude account
    "svce"<blob>="Claude Code-credentials"
$ test -f ~/.claude/.credentials.json   # → absent
```

There is **one** active credential item, keyed by unix user. No per-account
namespacing exists natively, which confirms the vault design: keep each account's blob
under `claudeswitch:<account-id>` and treat `Claude Code-credentials` as the single
"active" slot to write into.

Not yet verified: the JSON shape inside the blob, and whether a daemon can read it
without a GUI approval prompt. **Do this first in M2** — it's the one remaining
blocker on the whole vault design.

## 2. Account identity — `~/.claude.json` → `oauthAccount`

Live values (redacted):

```json
{ "accountUuid": "aaaaaaaa…", "organizationUuid": "11111111…",
  "emailAddress": "alice@example.com", "organizationRole": "admin",
  "organizationName": "alice@example.com's Organization",
  "organizationType": "claude_max",
  "organizationRateLimitTier": "default_claude_max_20x",
  "billingType": "stripe_subscription", "hasExtraUsageEnabled": false }
```

`organizationRateLimitTier` is the per-account limit profile — read it, don't ask me to
configure it. `~/.claude.json` is 162KB of mixed state, so **never rewrite it wholesale**:
patch the `oauthAccount` subtree only, atomically, with the rest preserved byte-for-byte.

Still to verify: whether the CLI rewrites `oauthAccount` itself after a credential swap
(making our patch redundant or racy), and whether a mismatch between credential and
`oauthAccount` is even detectable by the CLI.

## 3. Quota signals — the finding that matters

### `quotaLimits` in transcripts — REAL, but purely TRAILING

Assistant records in `~/.claude/projects/*/*.jsonl` carry a `quotaLimits` object:

```json
{ "status": "rejected", "resetsAt": 1788520200,
  "rateLimitType": "five_hour",           // also seen: "seven_day"
  "unifiedRateLimitFallbackAvailable": false,
  "overageStatus": "rejected", "overageDisabledReason": "org_level_disabled",
  "isUsingOverage": false,
  "lowPriorityOffer": …, "lowPriorityRetryAfterSeconds": …,   // occasionally
  "lowPriorityMaxWaitSeconds": …, "upgradePaths": … }
```

**Across 39 records in the last 14 days, `status` was `"rejected"` every single time.**
No `allowed`, no warning state, no remaining-percentage field, ever. This object is
written *only when the wall has already been hit*. It is an excellent trailing signal —
authoritative account, window type, and exact `resetsAt` (unix seconds) — and it is
useless as a trigger.

**Consequence: there is no local leading indicator. The daemon must compute one.**

### `message.usage` — the only leading signal available

Every assistant message carries full token accounting:

```json
{ "input_tokens": 2, "cache_creation_input_tokens": 448,
  "cache_read_input_tokens": 24100, "output_tokens": 259,
  "output_tokens_details": { "thinking_tokens": 187 },
  "service_tier": "standard", "speed": "standard",
  "cache_creation": { "ephemeral_1h_input_tokens": 448, "ephemeral_5m_input_tokens": 0 } }
```

Model is on the same record (`claude-opus-5` here), and records also carry `requestId`,
`apiBlockIndex`, and `effort`. `apiBlockIndex` looks like a 5-hour block counter —
**investigate it early**, it may hand you the window boundaries for free.

### Signals that turned out to be dead ends

| Source | Verdict |
|---|---|
| `~/.claude/stats-cache.json` | Daily message/session/tool **counts**, no tokens. Last computed 2026-08-28 — stale by 11 days. Useless. |
| `~/.claude/telemetry/` | Only `1p_failed_events.*` blobs, newest 2026-08-25. Useless. |
| `~/.claude/policy-limits.json` | Org policy restrictions (web-search isolation). Unrelated to quota. |
| `~/.claude/daemon-auth-status.json` | `{"status":"auth_required"}` from 2026-08-07, remote-control auth. Unrelated. |

## 4. Windows — BOTH are live, and the weekly one is currently binding

Confirmed `rateLimitType` values: **`five_hour`** (32 records) and **`seven_day`**
(7 records). `resetsAt` is a unix timestamp in seconds.

The most recent rejection in the data is a **`seven_day`** at `2026-09-09T02:31:12Z`.

This changes the product. A daemon that watches only the 5-hour window would have let
that one through. Weekly tracking is not a nice-to-have — it needs 7+ days of per-account
history, survives restarts, and is the constraint most likely to be what's actually
hurting. Rotation across 4 accounts helps the weekly ceiling far more than the 5h one.

## 5. Hot swap

Not independently verified here — taken as established from practice. Still needs the
contract test described in the build prompt, especially the *boundary* question (next
request vs. next turn) since the idle-gap switch policy depends on it.

## 6. Work accounts

Untested — no work account credential was available in this session. Test one before
building around the assumption that they can participate.

---

## The consequence for the design: self-calibration is mandatory

No published token budget exists locally, and the only "you're near the limit" signal
arrives *after* the limit. So:

1. Sum `message.usage` tokens per account, per window (`five_hour`, `seven_day`),
   from the transcripts.
2. Every `quotaLimits.status == "rejected"` event stamps an **empirical ceiling**: the
   token total accumulated in that window at the moment the account was refused.
3. That observed ceiling, per account and window type, becomes the denominator for the
   15% trigger. Until an account has produced at least one rejection, its ceiling is a
   *guess* — carry it as an explicit confidence level and be conservative.
4. `resetsAt` from the rejection gives exact window boundaries. Anchor the clock to it
   rather than inferring boundaries from first use.

This means **M1 has real work in it**: parse transcripts, bucket by window, back out the
ceilings, and show me the numbers. And it means the daemon gets *more accurate the more
it has seen you hit limits* — which should be stated plainly in the UI rather than
presented as precision it does not have.

---

# Round 2 — 2026-09-08, open items closed

## 7. The Keychain blob holds MCP tokens too — do NOT swap it wholesale

```
top-level keys: ['mcpOAuth', 'claudeAiOauth']       # 2282 chars
  mcpOAuth:
    notion|eac663db915250e7        { accessToken, refreshToken, expiresAt, clientId, … }
    plugin:slack:slack|38801a7d…   { accessToken, refreshToken, expiresAt, scope(465ch) }
  claudeAiOauth:
    accessToken           str(len=108)
    refreshToken          str(len=108)
    expiresAt             1788955166290 -> 2026-09-09T11:59Z   (+7.7h)
    refreshTokenExpiresAt 1791318666290 -> 2026-10-06T20:31Z   (+664h)
    scopes  ['user:file_upload','user:inference','user:mcp_servers',
             'user:profile','user:sessions:claude_code']
    subscriptionType, rateLimitTier ("…_20x")
```

**Design-critical.** The single Keychain item is shared: your Claude account credential
*and* your Notion and Slack MCP OAuth tokens live in the same blob. Swapping the whole
item on every rotation would log you out of every MCP server several times a day.

The executor must **merge, not replace**: take `claudeAiOauth` from the vaulted account,
keep `mcpOAuth` from the live item, write the union. The vault likewise stores only the
`claudeAiOauth` subtree per account.

**Token lifetimes matter for a 4-account rotation.** Access tokens last ~8h; refresh
tokens ~27 days. An account that sits idle longer than its refresh window is dead and
needs a manual re-login. The daemon must therefore refresh *idle* accounts on a schedule,
not only on the way in, and `doctor` must warn before a refresh token expires.

## 8. Calibration from transcripts does NOT work — negative result

Scale of the real data: **2,197 transcript files, 26,070 deduped billable records,
6.03 billion tokens over 21 days, 24 genuine limit hits** (6,290 raw rejection records
collapse to 24 once deduped by `(rateLimitType, resetsAt)` — every retry writes one).

I tested four weightings, asking which one makes the backed-out ceiling *consistent*
across independent rejections. A usable ceiling needs low spread. None delivered:

| weighting | five_hour (n=20) | seven_day (n=4) |
|---|---|---|
| raw sum, all tokens | median 118.3M, **CV 62%** | median 2205M, **CV 40%** |
| output tokens only | median 0.3M, CV 78% | median 5.4M, CV 45% |
| input+output, no cache | median 0.3M, CV 79% | median 5.4M, CV 45% |
| cost-weighted (cr×0.1, cc×1.25) | median 19.9M, CV 63% | median 336M, CV 42% |

A 40-62% spread cannot drive a 15% trigger. **The predictive model as specified is not
achievable from local data.** Likely causes: the server-side unit isn't a token count we
can reconstruct; transcripts for deleted projects are missing from the history; parallel
sessions and subagents undercount. Do not paper over this with a fudge factor.

## 9. But rejections are instantaneous and exact

Every rejection record has `model: "<synthetic>"` — Claude Code writes it locally, into
the transcript, at the moment of refusal. It carries the exact `rateLimitType` and
`resetsAt`. A daemon tailing transcripts learns of a limit hit within milliseconds, with
zero estimation error.

Combined with the hot swap, that is the real design: **react in the instant, don't
predict.** The cost of reacting is one refused request; the cost of predicting badly is
either wasted quota or a wall you didn't see coming.

## 10. The 5-hour window is rolling, not a fixed block

Two hits 14 minutes apart on 2026-08-25 reported `resetsAt` of 11:20 and then 10:50 — the
reset moved *earlier* as old usage aged out. Anchor the scheduler to each rejection's
`resetsAt` rather than computing block boundaries yourself.

Frequency for sizing: **24 limit hits in 21 days, ~1.1/day**, of which 4 were `seven_day`.

---

# Round 3 — 2026-09-08. SUPERSEDES findings #1 and #2

**Findings #1 and #2 above are wrong.** They concluded there is no leading quota signal
and that prediction is impossible. That is true only of *local* sources. It is false
overall: Claude Code's `/usage` command is backed by a server endpoint that returns exact
utilization on demand. I failed to trace it in round 1. The rest of the ground truth
(#3-#10) stands.

## 11. `GET https://api.anthropic.com/api/oauth/usage` — the leading signal

Found by string-scanning the binary (`~/.local/share/claude/versions/2.1.266`, a 199MB
Mach-O; also exposes `/api/claude_code/policy_limits`, `/api/oauth/profile`,
`/api/oauth/validate`).

```
GET https://api.anthropic.com/api/oauth/usage
  Authorization: Bearer <claudeAiOauth.accessToken>
  anthropic-beta: oauth-2025-04-20
  User-Agent: claude-cli/2.1.266 (external, cli)
-> 200
```

Response (live, redacted only where empty):

```json
{ "five_hour":  { "utilization": 27.0, "resets_at": "2026-09-09T08:50:00.497885+00:00",
                  "limit_dollars": null, "used_dollars": null, "locked_reason": null },
  "seven_day":  { "utilization": 27.0, "resets_at": "2026-09-09T12:00:00.497901+00:00" },
  "limits": [
    { "kind": "session",       "group": "session", "percent": 27, "severity": "normal",
      "is_active": true,  "resets_at": "2026-09-09T08:50:00Z" },
    { "kind": "weekly_all",    "group": "weekly",  "percent": 27, "severity": "normal",
      "is_active": false, "resets_at": "2026-09-09T12:00:00Z" },
    { "kind": "weekly_scoped", "group": "weekly",  "percent": 0,  "severity": "normal",
      "is_active": false, "resets_at": "2026-09-09T12:00:00Z" } ],
  "extra_usage": { "is_enabled": false, … },
  "spend": …, "seven_day_breakdown": …,
  "seven_day_opus": null, "seven_day_sonnet": null,   // per-model windows; null on this tier
  "member_dashboard_available": … }
```

**`utilization` is a percentage, exact, server-side, with a real `resets_at`.** The
`limits[]` array adds a `severity` field (`normal` observed; other values presumably
precede a rejection) and an `is_active` flag marking which window is currently binding.

### Why this is the whole ballgame

The endpoint authenticates with **whatever token you present**. The daemon holds every
account's token in the vault, so it can poll usage for **inactive accounts without
switching to them**. The hardest problem in the original design — "you can only observe
the account you're currently using" — does not exist. There is no projection, no decay
model, no calibration, and no confidence band. Every account's true utilization is one
cheap HTTP call away.

### Consequences

- The predictive design is restored, on exact numbers: switch when `utilization >= 85`
  (the 15% trigger), per window.
- **The 30% personal reserve is implementable exactly as originally specified**:
  personal is ineligible for overflow once its `utilization > 70`.
- The transcript rejection detector (#3) stays, demoted to a **safety net** — it catches
  anything the poller misses between intervals, and its `resetsAt` is authoritative.
- Both `five_hour` and `seven_day` come from one call. Weekly tracking is free.

### Still to verify

- Polling cost: does hitting this endpoint consume quota or count against a rate limit?
  Poll politely (start at 60s, back off on non-200) and confirm before tightening.
- What `severity` values exist besides `normal`, and whether one reliably precedes a
  rejection — that would be a better trigger than a fixed percentage.
- Whether `seven_day_opus` / `seven_day_sonnet` populate on other tiers; if a work account
  has per-model windows, the policy engine must handle them.
- Whether a refreshed or org-managed token works identically against this endpoint.

---

# Round 4 — 2026-09-08. Operational limits of the usage endpoint

Measured directly, by hitting it until it broke.

## 12. Polling is free, but tightly rate limited

```
call  http  5h_util  7d_util  latency
   1   200    34.0     29.0    222ms
   2   200    34.0     29.0    201ms
   3   200    34.0     29.0    258ms
   4   200    34.0     29.0    197ms
   5   200    34.0     29.0    225ms
   6   429   {"type":"rate_limit_error"}   retry-after: 299
   7-12 429  (still locked out)
```

- **Polling does not consume quota.** `utilization` was identical across five successive
  calls. (It reads 34/29 here versus 27/27 an hour earlier — that movement is real work
  done in between, which also confirms the numbers are live.)
- **The budget is roughly 5 calls, then a 299-second lockout.** `retry-after: 299` is
  returned on the 429. This is the single most important operational constraint on the
  daemon.
- **No rate-limit headers on a 200.** No `anthropic-ratelimit-*`, nothing to read ahead
  with. You only learn the budget by exhausting it, so the poller must be conservative by
  construction rather than reactive.
- **`retry-after` IS present on the 429**, so backoff can be exact rather than guessed.

### Poll budget math — this shapes the poller design

Four accounts at ~5 calls / 5 minutes means a full sweep of all accounts costs 80% of the
budget. A naive "poll everything every 30s" loop locks itself out within a minute and then
runs blind for five.

The saving insight: **an idle account's utilization only ever goes down.** It cannot rise
while nothing is using it, so it does not need frequent polling. Therefore:

- Poll the **active** account on a short interval (~60-90s), tightening as it nears 85%.
- Poll **inactive** accounts rarely and staggered (~10-15 min each), purely to learn when
  a burnt one has recovered.
- Poll a switch **target** immediately before switching to it — that call is worth its cost.
- Hold a hard token-bucket at 4 calls / 5 min across the whole daemon, leaving one spare
  for the pre-switch check. Never let scheduled polling consume the last call.

## 13. Response headers identify the account cheaply

```
anthropic-organization-id: 11111111-1111-1111-1111-111111111111
anthropic-workspace-id:    wrkspc_019eDPbgLj5a8GkF18CmzPR3
request-id:                req_011CesJnDQT57CnpqQBuFhyA
```

`anthropic-organization-id` comes back on every 200 and identifies which account the
presented token belongs to. **Use it to verify a swap took effect** — after writing the new
credential, one usage call whose org id matches the expected account is proof, and it
doubles as the fresh reading for the account you just moved to.

## 14. `severity` and the `limits[]` array

At 34% utilization `severity` reads `normal` on all three entries, and `is_active` marked
`kind: "session"` as the binding window.

**UPDATE 2026-09-09, observed live by the daemon:**

```
severity=warning  kind=session  percent=78
```

So `severity` is not a constant. The API moved the active session limit to `warning` at
**78%** utilization — seven points below the 85% trigger we chose. That is Anthropic's own
notion of "getting close", and it is a candidate for a better trigger than a number we
picked ourselves: it should track their thresholds even if those change.

**UPDATE, same evening — a third level exists.** Observed within the hour, on the same
account and the same `session` limit:

```
severity=normal                       (baseline, seen from 27% up)
severity=warning    percent=78
severity=warning    percent=88
severity=critical   percent=90
```

So the ladder is **normal → warning → critical → rejection**, and the API is telling us
plainly how close we are. Two things follow:

1. `warning` first appeared at 78% and persisted through 88%; `critical` at 90%. On this
   account (`default_claude_max_20x`), our chosen 85% trigger falls *inside* the warning
   band — which is reassuring, but it was luck rather than design.
2. `critical` at 90% is a far better hard-floor signal than our invented 96%, because it
   comes from the same system that will do the refusing.

Still unknown: whether these onsets are fixed percentages or tier-dependent, whether the
`weekly_all` and `weekly_scoped` limits use the same ladder, and whether `critical` always
precedes rejection with useful lead time. The daemon now records every transition to the
audit log (`kind: "severity"`, with the percent), so this answers itself over the next
few days. Until then the fixed percentages remain the trigger and severity is corroboration.


---

# Round 5 — 2026-09-09. End-to-end validation against a real limit hit

The daemon was running in dry-run when the account actually hit its 5-hour ceiling. This
is the loop the whole design depends on, observed rather than argued:

```
01:41:21  rejection  personal  five_hour  resets_at 2026-09-09T01:50:00-07:00
01:51:25  severity   personal  session    critical → normal  (percent 0)
```

Claude Code's own message in the terminal at that moment read
`You've hit your session limit · resets 1:50am (America/Los_Angeles)`. The detector's
`resets_at` matches it exactly, was read from the transcript within milliseconds of the
refusal, and needed no network call. Ten minutes later the poller watched the window clear
and severity fall from `critical` back to `normal` at 0%.

**This closes the M1 gate** ("do not start M3 until M1 has caught at least one real
rejection end to end"). The reactive safety net works on real data, and the severity ladder
observed earlier (normal → warning 78% → critical 90% → refusal ~100%) is now confirmed to
run all the way to an actual refusal and back.

## 15. The audit log needed de-duplication

377 `decision` rows accumulated in one evening, because the daemon evaluated every 20
seconds and recorded "stay" each time. The single `rejection` row — the only one that
mattered — was buried among them, and the file would grow by roughly 4,300 rows a day.

Fixed by recording a decision only when it differs from the previous one. Rejections,
switches, severity transitions and errors are always recorded; unchanged "stay" is not.
A lesson worth generalising: an audit log that records everything records nothing.


---

# Round 6 — 2026-09-09. Vaulted credentials rot, and the vault must handle it

## 16. Refreshing REVOKES the old access token

A credential vaulted at 22:39 was tested against the usage API at 02:40 the next morning:

```
GET /api/oauth/usage  with the vaulted access token
-> 401 {"type":"authentication_error",
        "message":"OAuth access token has been revoked."}
```

Not expired — **revoked**. In between, Claude Code refreshed the live session, and that
refresh invalidated the previously issued access token. The refresh token had rotated too
(vaulted `…PwAA`, live `…BgAA`).

**This is the most consequential finding since the usage endpoint.** A vault that stores a
snapshot does not work. An entry goes dead as soon as that account's live session
refreshes — roughly every 8 hours for an account in use, and by expiry for one that is
not. A rotation target that 401s is worse than no rotation target, because the policy
engine believes it is available right up to the moment it is needed.

### What the vault must therefore do

1. **Refresh vaulted tokens itself**, on a schedule, and write the new pair back to the
   vault immediately — a rotated refresh token that is not persisted bricks the entry.
2. **Re-capture from live** whenever an account is the active one, so the vault copy never
   drifts behind the session.
3. **Write back on every observed refresh**, including one performed by Claude Code: if the
   live item's `claudeAiOauth` no longer matches the vault entry for the active account,
   the vault is stale and must be updated.
4. **Verify before switching, not just after.** The pre-switch usage call already reserved
   in the call budget is exactly this check; a 401 there means "re-login needed", and the
   account must be marked unavailable rather than swapped to.

The endpoint for (1) is `POST /v1/oauth/token` (found in the binary alongside
`grant_type`, `refresh_token`, `client_id`, `authorization_code`). **Do not exercise it
against a live credential casually**: a refresh rotates and revokes the current pair, so a
refresh performed outside Claude Code without writing the result back into the live
Keychain item will break the running session.

## 17. Vaulting whatever is live is easy to do by mistake

`claudeswitch add acme-work` was run without the intended `/login` having switched
accounts. It succeeded, verified against the usage API, and produced a vault entry that
looked entirely healthy — but its org id was the personal account's. The result is two
names for one account: `status` would show two rotation targets, the policy engine would
"rotate" between identical credentials, and nothing would actually be gained.

Fixed: vault entries now carry a `claudeswitchMeta` annotation (org id, account id, when
it was vaulted), and `add` refuses when the credential's org already belongs to another
entry, naming the conflict and what to do instead. The annotation is stripped in
`MergeForSwap` so it never reaches the item Claude Code reads.

Ordinary care would not have caught this: the failure is silent and the output looks
correct. The org id is the only thing that distinguishes them.

---

# Round 7 — 2026-09-09. A second account, and the bug it exposed

## 18. Work accounts CAN participate — and identity is the organization, not the email

`claude auth login --sso` on the work account succeeded. The result:

```
signed in as  alice@example.com
organization  Acme
org id        22222222-2222-2222-2222-222222222222
plan          team via claude.ai
tier          default_claude_max_5x
refresh token issued, expires in 27d
```

Three things settle open questions:

1. **SSO accounts do get a refresh token** (27 days, same as the personal account), so the
   vault can keep them alive. The feared "short-lived access token only" case did not occur.
2. **The email is identical to the personal account's.** Only the organization differs
   (`22222222…` Acme team vs `11111111…` personal Max). Any identity scheme based on email
   would have merged two distinct quota pools. The org-id-based attribution was right for a
   reason that had not been anticipated.
3. **The work tier is LOWER**: `default_claude_max_5x` against the personal account's
   `default_claude_max_20x`. Work-first rotation therefore burns the smaller allowance
   first — worth deciding deliberately rather than by default.

## 19. `SyncActive` destroyed a vaulted credential

The daemon log, minutes after the SSO login:

```
18:06:42 INFO vault entry re-captured from the live credential
              (Claude Code had refreshed it)  account=personal
```

It had not been refreshed. The user had signed in to a *different* account. `SyncActive`
compared the vaulted `personal` token to the live token, saw a difference, concluded
"refreshed", and overwrote the personal vault entry with the Acme credential. The entry
was left internally inconsistent: metadata claiming org `11111111…`, credential belonging
to `22222222…`.

**The reasoning error:** two tokens differing means one of two very different things —
the same account was refreshed (re-capture is correct and necessary), or a different
account is now live (re-capture destroys the entry). Nothing in the credential itself
distinguishes them. The organization must be checked, and it now is, at the cost of one
API call on the rare occasions the tokens differ.

Two earlier decisions limited the damage, which is the argument for both:

- the swap's org verification would have refused to install it and rolled back
- the `claudeswitchMeta` annotation preserved the truth, making the corruption detectable

The credential itself was not recoverable: the `PRESWAP-BACKUP` copy held the token that
had already been revoked (Round 6), so the only cure was a fresh login.

## 20. Two more instances of the same root cause: trusting stored state over observation

The same investigation turned up two more places where a *stored belief* was preferred to
an *observation*:

- **`state.Active` was CLI-owned**, so the daemon's correct observation of which account
  was live got overwritten by the stale stored value on every save — meaning a wrong
  attribution could never self-correct. Active is not a preference; it is whichever
  account the live credential belongs to. Now daemon-owned.
- **`refresh` decided "is this the active account?" from `state.Active`.** With state
  cleared, it would have happily refreshed — and revoked — the token of the running
  session. Now answered by comparing the vault entry against the live Keychain item, and
  when that cannot be determined it assumes the account *is* live, so the caller is asked
  rather than acted upon.

The generalisable lesson, which the design already claimed to follow and did not:
**where reality is observable, observe it. Never infer it from something you wrote down
earlier.** Attribution got this right; three other places did not.

---

# Round 8 — 2026-09-09. Refresh proven; and a third org

## 21. The refresh path works, including for SSO accounts

Run against the vaulted Acme (SSO, team, `max_5x`) account while the live session was on
a different account:

```
$ claudeswitch refresh work-main-personal
  ✓ refreshed work-main-personal
    org           22222222-2222-2222-2222-222222222222
    new expiry    in 8h0m0s
    access token  …GgAA → (new, stored)
    refresh token rotated and persisted
```

Verified afterwards:

- the new access token answers the usage API (200, 5h 28% / 7d 16%)
- **the refresh token DID rotate**, confirming Round 6 — a refresh whose result is not
  persisted would have destroyed the account
- the live Keychain item was untouched (`…pgAA` before and after), as were both MCP tokens
- `claude auth status` still reported the personal org: the running session never noticed

So a vaulted account can be kept alive indefinitely, SSO included. `POST
https://platform.claude.com/v1/oauth/token` with `grant_type=refresh_token` and
`client_id=9d1c250a-e61b-44d9-88ed-5944d1962f5e` is the whole of it.

## 22. Inactive-account polling proven with two real accounts

```
ACCOUNT      5-HOUR          7-DAY           STATE
work-main  28%  4h04m      16%  154h       available
personal     15%  2h54m      16%  154h       ACTIVE available
```

`work-main` was read using its vaulted token while the live session belonged to
`personal`. This is the fact the entire predictive design rests on, and it is now
demonstrated rather than inferred.

## 23. A third organization, and `add` vaulted the wrong account

During the re-login, `claudeswitch add personal` stored a credential belonging to
organization `33333333-3333-3333-3333-333333333333` — neither the personal Max org
(`11111111…`) nor the Acme team org (`22222222…`). The browser login had landed on yet
another account, and `add` accepted it without complaint.

That account turns out to be **exhausted**: `weekly_all` at 100%, `severity: critical`,
not resetting until 2026-09-13. It would have been vaulted as a healthy-looking rotation
target that could never be rotated into.

**The defect was in the guard, not the user.** `add` checked the credential against
*other* vault entries (the duplicate check from Round 7) but never against the
organization the config pins for *the account being added*. Config said
`personal.org_id = 11111111…`; the credential said `33333333…`; nothing compared them.

Fixed: `add` now refuses when the live credential's organization does not match the
account's configured `org_id`, and its output leads with the human-readable identity
(`account`, `organization`) from `claude auth status` rather than only a uuid, because a
uuid is not something anyone checks at a glance.

Three org ids are now known to exist under the single email `alice@example.com`:

| org | name | tier | note |
|---|---|---|---|
| `11111111…` | alice@example.com's Organization | `max_20x` | vaulted as `personal` |
| `22222222…` | Acme | `max_5x` (team) | vaulted as `work-main` |
| `33333333…` | unknown | `max_20x` | exhausted until 09-13, not vaulted |

This is further argument for org-id identity: the email distinguishes none of them.

## 24. Which organization a login lands on is decided by the browser, not the CLI

All three organizations share the single email `alice@example.com`, so `claude auth login`
cannot be steered by `--email`. Observed across five logins:

- plain `claude auth login` → `11111111…` (personal Max) three times, `33333333…` once
- `claude auth login --sso` → `22222222…` (Acme team)

The OAuth flow issues a credential for whichever organization the browser session is
currently in. `--sso` effectively selects the SSO-backed org; otherwise you get the
browser's active one. **To land on a specific organization, switch to it on claude.ai
first, then run the login.** `claudeswitch whoami` afterwards is the check, and `add` now
refuses a credential whose org does not match the account's configured `org_id`.

## 25. The poller was charging its call budget for accounts it could not poll

`Tick` spent a token-bucket slot *before* resolving the credential, so each configured
account with no vault entry consumed one of the three scheduled calls per five-minute
window and then failed locally without making an API request. With two placeholder
accounts configured, the daemon was polling at roughly a third of its intended rate while
appearing to respect the budget.

Fixed by resolving the credential first and skipping unvaultable accounts without
charging anything. Their status line now reads `not set up — run claudeswitch add <id>`
rather than a truncated Keychain error, because an account that was never configured is
not a fault to report at the user.

---

# Round 9 — 2026-09-09. First real rotation, and what it exposed

## 26. The daemon performed a live rotation

With the trigger temporarily lowered to 20% and the session manually placed on the Acme
account (28%), the daemon rotated unaided:

```
20:04:52 INFO swapped account  account=personal org=11111111… five_hour=16 mcp_preserved=true
20:04:52 INFO switched from=work-team to=personal
              because="active account at 28%, over the 20% trigger"
```

Detected, decided, swapped, verified by organization id, MCP tokens preserved, audited.
The whole product, working, on real credentials.

## 27. `claude auth status` reports a CACHE, not the live credential

This finally answers the question left open in §2. The `oauthAccount` block in
`~/.claude.json` is written at login and **does not move when the credential is swapped**.
Observed directly: with the Acme credential installed and answering the usage API as
organization `22222222…`, `claude auth status` still reported `11111111…`.

Consequences:

- `claudeswitch whoami` was giving a confidently wrong answer, which is worse than no
  answer given it was the recommended pre-vault check. It now reads the organization from
  the credential via the usage API and shows the cached label only as a label, warning when
  the two disagree.
- Anything identifying an account must use the credential, never Claude Code's cached view.
- Claude Code apparently tolerates the disagreement: the credential governs API access, the
  cached block only labels it.

## 28. The rate budget was not shared, so it was not a budget

`internal/vault` made five usage-API calls — store, swap-verify, refresh-verify, sync and
verify — none of which consulted the poller's token bucket. The endpoint's limit applies to
the machine, so the daemon's 4-per-5-minutes plus every `use`, `add` and `refresh` together
blew past it. The result was a 429 storm, which starved the poller, which broke attribution,
which left the daemon making decisions about the wrong account.

Fixed with a single process-wide `usage.Shared()` budget that every caller goes through.

## 29. A 429 with `Retry-After: 0` produced no backoff

The observed 429s carried `retry-after: 0`, which was honoured literally: `Penalize(0)`
set a lockout that had already expired, so the daemon kept calling — logging a warning
every eighty seconds while making the rate limiting worse. Backoff is now
`max(Retry-After, 60s)`.

The general rule: a backoff computed from untrusted input needs a floor.

## 30. Attribution ran only at startup

`PollActive` was called once when the daemon began. If that call was rate limited — which
§28 made likely — the daemon never learned which account was live and every subsequent
decision was about the wrong one. It now re-derives on every save tick, and reports failure
instead of silently keeping a stale attribution.

Same lesson as Round 7, third instance: **observe continuously, not once.**

---

# Round 10 — 2026-09-09. Reusing an id showed one account's usage under another's name

Renaming `work-team` (organization `22222222…`) to `work-team`, and then giving the
freed name to a *different* account (`33333333…`), produced a display like this:

```
work-team         30%  17%   available
work-team  30%  17%   available     <- not vaulted, and really at 100%
```

Both rows were the same record. Two independent faults combined:

1. **`forget` and `rename` deletions were undone by the state merge.** The CLI merge
   copied every account from disk back over the in-memory state, so a record removed a
   moment earlier reappeared on the very next save. The operations reported success and
   changed nothing durable.
2. **Nothing checked that a state record belonged to the account it was filed under.** The
   surviving record described organization `22222222…` but now sat under an id the config
   pinned to `33333333…`.

The consequence was not cosmetic: the policy engine would have read an exhausted account
(100% weekly, no reset until 09-13) as having 70% headroom and rotated into it.

Fixed in two places. `State.Drop` records deliberate removals so a merge cannot resurrect
them, and `State.Reconcile` — run on every load — discards any record whose organization
disagrees with the id's configured `org_id`, along with records for accounts no longer
configured. A record that cannot be shown to describe the right account is thrown away
rather than trusted.

**Labels are gone.** An account had an id (`work-team`) and a display label
(`work-team`); `status` showed the label, every command took the id, and a name that
looked free was already in use — which is what prompted this whole episode. One account,
one name. A config that still sets a divergent `label` is now a validation error, and
table columns size themselves to the widest id.

## 31. The authorize endpoint validates `state` length; organization_uuid remains untested

**This section originally claimed `organization_uuid` was rejected. That claim was wrong**
— or at least unproven — and is corrected here.

Two authorize requests were refused with "invalid request format". The first carried
`organization_uuid`; the second did not, and was still refused. Diffing the URL against one
Claude Code itself printed found the real difference:

```
claude auth login   state=KQjaDaU-fggv2L0f0AWv9G8oDL7AKtNPkXlikZJSnBA   (43 chars, 32 bytes)
claudeswitch        state=JHwotvvqOB4gcKl_Iz6eiw                        (22 chars, 16 bytes)
```

**A 16-byte `state` is refused; 32 bytes is accepted.** The endpoint validates its length
rather than merely echoing it. With that corrected the flow completed end to end: the
authorize page loaded, the code exchanged at `/v1/oauth/token`, and a working credential
came back.

So the first failure is explained by the short state.

**Tested afterwards, properly: `organization_uuid` is ACCEPTED AND IGNORED.** With a valid
32-byte state and the parameter present, the authorize page loaded, the code exchanged
cleanly, and the credential came back for the browser's organization (`11111111…`), not the
one requested (`33333333…`). Accepted-and-ignored is a different fact from rejected, and
worth stating precisely: the parameter costs nothing and buys nothing.

**Conclusion: which organization a login returns is decided by the BROWSER SESSION, and
nothing in the request can override it.** Confirmed against three parameters, all of which
are accepted and silently ignored: `organization_uuid`, `prompt=select_account`, and
`prompt=login`. Only `--sso` behaves differently, and only because it selects the
SSO-backed org.

**What does work: a different browser application.** After eight failed attempts — through
`claude auth login`, through claudeswitch's own flow, with an organization switcher, and
with a private window — opening the authorize URL in a *separate browser application*
signed in as the other account returned that account's credential first time. Separate
applications have separate cookie jars; a private window evidently does not go far enough.

So the working recipe for onboarding an account is:

1. `claudeswitch login <id> --direct` — prints an authorize URL, touches nothing
2. open it in a browser you are not normally signed into, as the account you want
3. `claudeswitch login <id> --code <code>` — verifies the organization and vaults it

The organization only has to be *reached*; it never has to be *chosen*, because the
wrong-account guard refuses anything that does not match.

The parameters, in the order Claude Code sends them, all of which matter:

So **there is no known way to choose the organization from the CLI.** `claude auth login`
returns whichever organization the browser session is in; `--sso` selects the SSO-backed
one; nothing else steers it. Three consecutive attempts on this machine returned the
personal organization regardless of what claude.ai was displaying.

The parameters that DO work, in the order Claude Code sends them:

```
code=true, client_id, response_type=code, redirect_uri, scope,
code_challenge, code_challenge_method=S256, state
```

`claudeswitch login --direct` uses exactly these. It still cannot pick the organization, but
it keeps the one advantage that matters: the credential is obtained and vaulted **without
ever being installed as the live one**, so onboarding a second account no longer means
logging out of the first and swapping back. `--pin-org` retains the rejected parameter
behind an opt-in, so the finding stays testable if the endpoint changes.

---

# Round 11 — 2026-09-10. An organization is not a quota pool

## 32. Quota is per SEAT, not per organization

Noticed because the numbers disagreed with what Claude Code showed. Two credentials, both
for organization `22222222` (the Acme team org), queried minutes apart:

```
alice@example.com   5h 23%   7d 19%
alice@example.com   5h  3%   7d  0%
```

Same organization, entirely different utilization. A team organization has one **seat** per
member, and each seat has its own limits. `/api/oauth/usage` reports the seat behind the
token presented — not the organization's aggregate.

**This invalidated the identity model.** Everything keyed on `anthropic-organization-id`,
which the usage API returns in a header, on the assumption that an organization identified
an account. It does not.

`GET /api/oauth/profile` supplies the real identity:

```json
{ "account":      { "uuid": "bbbbbbbb-…", "email": "alice@example.com", "display_name": "Alice" },
  "organization": { "uuid": "22222222-…", "name": "Acme" },
  "application":  { "uuid": "9d1c250a-…", "name": "Claude Code" } }
```

`account.uuid` is the quota pool. The three vaulted accounts turn out to be three different
people:

| entry | seat | email | organization |
|---|---|---|---|
| `personal` | `aaaaaaaa` | alice@example.com | `11111111` |
| `work-team` | `bbbbbbbb` | alice@example.com | `22222222` (Acme) |
| `personal` | `cccccccc` | bob@personal.example | `33333333` |

### What the wrong model caused

- **`SyncActive` replaced one member's credential with another's.** A `/login` as
  alice@example.com produced a token that differed from the vaulted one; the organization
  matched, so it was recorded as "the same account, refreshed". The alice@example.com
  credential for that organization was overwritten and is gone.
- **`add` refused legitimate accounts as duplicates.** Two members of one organization were
  reported as "the same Claude account already vaulted as …". Several of the failed
  onboarding attempts on 2026-09-09 may have been this, not the browser.
- **Rotation could have counted one pool twice**, had two seats of one organization been
  vaulted under different names.

### The fix

Vault entries record `accountUuid` and `email` alongside the organization. Identity
comparisons — `SyncActive`, the duplicate check in `add` and `StoreTokens`, attribution —
all use the seat. An entry with no recorded seat is refused rather than assumed to match,
and `claudeswitch identify` backfills existing entries by asking each stored credential who
it belongs to (no logins needed). `accounts` and `whoami` show the email, because a uuid is
not something a person can check at a glance.

## 33. Identity is the PAIR (person, organization) — neither half suffices

§32 concluded the seat was `account.uuid`. That was half right, and the other half showed
up within the hour. The same person in two organizations also has two separate pools:

```
alice@example.com in org 11111111   5h  0%   7d 46%
alice@example.com in org 22222222   5h 23%   7d 19%   (at a time the first read 85%/44%)
```

So a quota pool is one person **within** one organization, and the four accounts now
vaulted demonstrate both failure modes at once:

| entry | person | organization |
|---|---|---|
| `work-team` | alice@example.com | `11111111` |
| `work-main` | alice@example.com | `22222222` |
| `personal` | alice@example.com | `22222222` |
| `personal` | bob@personal.example | `33333333` |

Rows 1–2 share a person; rows 2–3 share an organization. Comparing organizations alone
merges 2 and 3; comparing people alone merges 1 and 2. Identity is `account.uuid@org.uuid`,
and `work-main` could only be vaulted once that was true — the person-only check
refused it as a duplicate of `work-team`.

Accounts under one organization may also hold different subscriptions and plans, which is
the same fact from the billing side.

**The general lesson, for the third time this build:** an identifier that is *available*
is not automatically the identifier that is *correct*. `anthropic-organization-id` came
back on every usage response, which made it feel authoritative; it was simply the wrong
granularity, and nothing failed loudly until two seats of one organization appeared.

---

# Round 12 — 2026-09-10. The rate limit was misread, and the budget was per-process

## 34. The usage endpoint's limit is a BURST allowance, not a sustained cap

§12 recorded five rapid calls followed by a 429 with `retry-after: 299`, and concluded
"roughly 5 calls per 5 minutes". **That conclusion was wrong**, and it governed the
daemon's entire polling cadence for two days.

Measured again, one call every 20 seconds:

```
 1  200  t+000s      7  200  t+120s
 2  200  t+020s      8  200  t+140s
 3  200  t+040s      9  200  t+160s
 4  200  t+060s     10  200  t+180s
 5  200  t+080s     11  200  t+200s
 6  200  t+100s     12  200  t+220s
```

Twelve consecutive successes. Calls 6 through 12 all fall inside a single five-minute
span, so a fixed 5-per-5-minute window cannot be what is happening. The limit is a burst
allowance that refills: a tight loop trips it, a steady ~15 calls per 5 minutes does not.

### Why the wrong reading survived so long

Because a second bug kept producing 429s that appeared to confirm it. `usage.Shared()` was
a **per-process** singleton, so the daemon held one allowance and every `cs status`,
`cs plan` and `cs identify` held another. The real spend was several times what any of them
believed, the daemon's polls began failing, and the low figure looked vindicated.

Two wrong things propping each other up: an over-tight budget that made 429s likely, and a
per-process budget that made them inevitable, each taken as evidence for the other.

### What changed

- The budget is now backed by a lock-guarded file under `~/.local/state/claudeswitch`, so
  every process shares one allowance. Falling back to process-local on any file error is
  deliberate: more conservative than none.
- The allowance is 12 per 5 minutes and configurable (`api_budget`), because the ceiling
  was measured imprecisely and anyone who learns better should be able to say so.
- Polling went from 4 minutes to **60 seconds** for the active account, 30 seconds when
  it is near the trigger or burning fast, 10 minutes for idle accounts.
- `validate()` computes calls-per-window from the configured cadence and refuses a config
  that would trip the burst guard, naming which knob to turn.

## 35. A stale reading was acted on silently

The same incident: the account sat at 93% with no rotation. The daemon's polls were
failing, so it was deciding on a reading minutes old — and "stay" is logged at debug level,
so it said nothing at all while doing it.

It now warns when the reading behind a decision is older than three poll intervals, naming
the age and the last error. A daemon that cannot see should say so rather than carry on
quietly.

---

# Round 13 — 2026-09-16. The seat rule was written down, and still not applied

§33 settled that a quota pool is one person **within** one organization, and that identity
is `account.uuid@org.uuid`. Six days later, five separate places were still storing,
comparing, or displaying one half of that. None of what follows is a new discovery about
the API; it is the cost of a rule being known and not enforced.

(Account names have been reused across rounds. §33's `personal` is this round's
`work-team`; the uuids are the stable identifiers.)

## 36. Our own backoff refused the commands that end the shortage arming it

Every account was exhausted. The 429s that produced armed a twenty-minute lockout shared by
every caller — and `add`, the command that relieves a shortage by adding capacity, was
refused for the whole of it:

```
claudeswitch: paused by our own call budget, retry in 15m1s
              — wait it out and try again; the credential was NOT vaulted
```

Worse than a delay: the credential `add` vaults comes from an interactive login, so the
refusal discarded the expensive step and the login had to be done again. `whoami`, `doctor`
and `setup` were refused on the same grounds — the whole recovery toolkit, disabled by the
condition it exists to recover from.

The lockout is armed by a 429 against whichever token happened to be polling, but the rate
limit behind it belongs to **that account**. Applied to a credential that has made no calls
at all it is not a safety measure, it is a guess carried over from someone else.

### What changed

- Calls carry a priority rather than a bool. `Scheduled` polling keeps a call in reserve,
  `Swap` may spend it, `Interactive` is never refused by our own bookkeeping.
- Interactive calls are still paced and still recorded against the window, so a bypass
  cannot hide spend from the daemon. A burst of them defers scheduled polling by up to one
  window, which is the intended trade.
- `add` no longer refuses a credential because the *usage* endpoint is rate limited. That
  read is a liveness check, and a 429 shows the token reached the API and was recognised —
  nearly the opposite of a reason to reject it. The only value taken from it is the
  organization id, which the profile carries too.

## 37. The config block `add` recommended switched off every seat check

The block `add` printed pinned on the organization alone:

```
    [[account]]
    id     = "personal"
    org_id = "44444444-4444-4444-4444-444444444444"
```

`config.Account.Seat()` returns empty unless **both** fields are set, and every integrity
check in `State.Reconcile` is gated on a non-empty seat. So pasting the recommended block
did not pin loosely — it disabled the detection of a credential filed under the wrong name
outright, which is the failure that took out three accounts on 2026-09-11.

The seat was read during `Store` and written into the keychain annotation all along.
`Entry` simply never carried it back out, so the command held the right answer in memory
while printing the wrong one.

### What changed

- `add` prints a complete, seat-pinned block and offers to write it, with the priority
  entry, rather than asking for it to be retyped. A seat uuid is only knowable after
  signing in, so this was never a file anyone could write correctly in advance — which is
  why `setup` has always written its own.
- The edit is textual, so a hand-maintained config keeps its comments byte for byte. It is
  parsed back before replacing the original, and nothing is prompted when stdin is not a
  terminal: a pipe reaches EOF immediately and a yes/no default would have written to
  someone's config because their terminal was not attached.

## 38. Two names for one pool, and a `remove` that could not undo it

`add` will vault an account the config does not list, and says so while doing it. The
duplicate-credential checks enumerated the **config**. So the entries `add` itself created
were invisible to the one check that exists to stop a second name being given to a pool
that already has one:

```
cs add work-team       # not in config — vaulted anyway, and invisible to the check
cs add personal   # same seat, no conflict reported
```

Both then report identical utilization, so rotating between them does nothing, and because
a refresh revokes the token it was given, refreshing one destroys the other.

Repair was impossible by the command that repairs things. `remove` refuses to delete the
entry holding the live credential, and liveness is decided by comparing access tokens — so
two entries holding one credential are **both** live at once. Switching to the other could
not release the first, because the other held the identical token:

```
cs remove work-team    → "work-team" holds the credential Claude Code is using right now
cs use personal   → ✓ now using personal
cs remove work-team    → "work-team" holds the credential Claude Code is using right now
```

### What changed

- Vaulted ids are recorded in the state file as they are stored. The keychain cannot be
  enumerated without a dump that prompts for every item, so what we store is what we
  remember storing. `Reconcile` leaves that record alone: "not in the config" is precisely
  the case it exists to remember.
- Both `doctor`'s check and `add`'s conflict check now consider the union of configured and
  vaulted ids.
- `remove` allows deleting one copy of a duplicate — the credential stays vaulted under the
  other name, the live session is untouched, and it is the only way out — and it names the
  entry keeping the credential rather than quietly proceeding.

## 39. Abbreviating a seat keeps the half that matches

A seat is `person@organization`, and `short()` takes the first eight characters — which is
the person and nothing else. So the messages whose entire purpose is to contrast two seats
named the half that matched and cut off the half that differed:

```
work-team holds a credential for seat bbbbbbbb, but is pinned to bbbbbbbb
the account currently signed in is alice@example.com, but "work-team" is pinned to seat bbbbbbbb
```

Both read as self-contradictions, and they did it in the ordinary case rather than a corner
one: §33's whole point is that the same person in two organizations is two pools, so the
organization is exactly what such a message is contrasting — and exactly what was being
discarded. `login`'s pre-flight warning called a seat an "organization" outright, setting
the same trap one step earlier.

### What changed

- `shortSeat()` abbreviates both halves: `bbbbbbbb@22222222`.
- The refusal now says how to land on the organization you want, which it never did. Per
  §24 the login follows the browser's active organization and cannot be asked for one, so
  switch it on claude.ai first, or use `--sso` for an SSO-backed org. That sentence is what
  the whole round turned on: the block was never the duplicate check, it was the login
  landing somewhere else.

## 40. The refusals follow the live account, and the daemon was not in the shared budget

On 2026-09-16 `daemon.log` held a steady "usage API refused us" through the day, every one a
429 with `Retry-After: 0`. Grouped by hour and account, each landed on **whichever account
was live**: `work-main` until it was swapped out at 08:38, `personal` (and the
pre-attribution `active`, the same credential) from then on. The four idle accounts, polled
every ten minutes, drew none.

Claude Code itself calls the same endpoint with the same credential. Its binary
(2.1.273) carries a `fetchUtilization` that issues `GET /api/oauth/usage` — plain, and
with `?at_wall=1&skip_spend=1` — authenticated as the live account, from `/usage`, the
extra-usage and usage-credit checks, two at-limit status reads and the purchase flow. So
the live account's allowance is spent by callers this program cannot see. How often they
call was not measured; that they call is established from the code.

Reading the budget to explain it turned up two faults of our own:

- **The poller never used the shared budget.** §34 made the budget a file shared across
  processes and added `sharedBudgetFor()` to the poller — which nothing called.
  `poller.New` still built a private `usage.NewBudget()`, so the daemon's polls, most of
  the program's spend, never appeared in `api-calls.json`. Every other caller budgeted
  against a ledger that left the daemon out.
- **Every poll was counted twice.** `Tick`, `RefreshStale` and `RefreshCandidates` each
  asked the budget before calling `fetchInto`, which asked again.

And one fault of design: a 429 set **one** lock for every account. The account refused
most is the live one, for the reason above, so each of its refusals also stopped the
reading of every idle account — the figures a rotation decides on.

### What changed

- `poller.New` uses the shared budget. `fetchInto` is the only place a poll consults it.
- The call window stays one for the machine: it is the burst guard. The lock is per
  credential, keyed in the ledger by a digest of the token, so the live credential is one
  entry however it was reached and the file never holds a token. A refused account waits;
  the others are read on schedule. `Tick` moves on to the next due account instead of
  ending the tick.
- `status` and the session context say how many accounts are backing off rather than
  implying they all are, and a stale row is blamed on a lock only when that row was refused.

## 41. A token refresh raised a prompt nobody saw, and the keychain stopped answering for an hour

After the daemon was rebuilt on 2026-09-16, it refreshed two idle accounts overnight
(`work-team` at 23:53, `personal` at 03:46). Both logged:

```
CREDENTIAL AT RISK: refreshed "work-team" but could not store the new token
(writing keychain item "claudeswitch:work-team" timed out — approval is probably
being asked for …). The old token is now revoked; run `claude` and /login for this account
```

Both accounts went on polling successfully with their vaulted tokens — `work-team`
still at 04:44, five hours after a refresh triggered by the old token being within an hour
of expiry. The stored token was the new one. The unified log says why:

```
23:53:29.877 security   SecACLSetSimpleContents
23:53:29.886 security   SecKeychainItemModifyContent        ← the token, stored
23:53:29.915 security   SecACLSetSimpleContents
23:53:29.918 securityd  displaying keychain prompt for /usr/bin/security(60438); ACL: …
```

`credstore.Write` passed `-T <claudeswitch path>` on every write. On an existing item that
is an access-list change, and an access-list change always asks. The content was written
first; the process then waited on the prompt, was killed at the 30-second timeout, and the
caller reported the whole write as failed.

Between 19:40 and 04:50 securityd displayed five prompts, every one an access-list change
on a write. None was for a read: reads go through `/usr/bin/security`, and that is the
program the access list is checked against, not claudeswitch.

The unanswered prompt did more than mislabel a credential. From 00:02 securityd logged
`securityd has reached its thread limit (100) - service deadlock is possible`, and no
`SecKeychainItemCopyContent` from any `security` process completed until 01:06. The daemon
could not read the live credential, went blind, and the watchdog restarted it three times
(00:13, 00:38, 01:06). Every other keychain user on the machine — Claude Code included — was
equally unable to read for that hour. The link from the pending prompt to the thread limit
is inferred from the timing; the thread-limit messages and the hour without a read are
logged.

### What changed

- A vault item is given `-T` when it is created, never when it is updated. Refreshing a
  token changes only its content, which raises no prompt.
- A write that times out is read back before being reported. If the new token is there,
  the write succeeded, whatever happened to the command afterwards.
