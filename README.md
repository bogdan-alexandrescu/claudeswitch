# claudeswitch

Keeps Claude Code pointed at an account that still has quota.

**Working and live.** It observes every account you own, reports on them, switches on
command, and rotates automatically before you hit a wall. For the plan and the evidence
behind it, see
[`BUILD_PROMPT.md`](BUILD_PROMPT.md) for the plan and
[`docs/GROUND_TRUTH.md`](docs/GROUND_TRUTH.md) for the measured facts it is
built on.

## Why it exists

Hitting the weekly ceiling blocks work until it resets — days, sometimes. Four
accounts sit idle while one is exhausted. This watches all of them and (from M3)
moves you onto a fresh one before you notice.

## Install

```sh
go build -o bin/claudeswitch ./cmd/claudeswitch
./bin/claudeswitch setup         # vault your accounts, write the config, install
```

`setup` is interactive and does the whole first run. It finds the account you
are already signed into, walks each additional one through a browser that will
actually produce a different account, writes a config with the real seats pinned,
and offers to install the daemon in dry-run.

Pinning cannot be done by hand in advance: you only learn an account's seat uuid
by signing in to it, which is why the config is generated rather than templated.

`init` still writes a bare config if you would rather fill it in yourself, and
`doctor` checks everything that has to be true.

`install.sh` also puts a `cs` symlink next to the binary, so every command below
works as `cs status`, `cs plan`, and so on. It is a symlink rather than a shell
alias deliberately: aliases do not exist in non-interactive shells or scripts.

## Use

```sh
claudeswitch status              # every account's real utilization, both windows
claudeswitch accounts            # what is in the vault
claudeswitch add <id>            # vault the credential that is live right now
claudeswitch use <id>            # swap onto a vaulted account (hot; no restart)
claudeswitch history -days 21    # deduped rejection history from your transcripts
claudeswitch session             # token usage across every account used in a span
claudeswitch watch               # foreground: poll + watch for refusals
claudeswitch doctor              # keychain, usage API, transcripts, config
```

`status` on a machine with one account configured:

```
  ACCOUNT      5-HOUR                          7-DAY                           RESETS    STATE
  personal     [#########.............]  40.0% [#######...............]  30.0% 3h10m     ACTIVE available
                 ↳ binding: session (normal) 40%, resets 3h10m

  thresholds  switch ≥85%   hard floor ≥96%   swap idle
  api budget  2 scheduled call(s) available now (4 per 5m0s, one held for swaps)
```

## Adding an account

```sh
claudeswitch login work-a          # sign in, verify, vault, switch back
claudeswitch login work-a --sso    # ...via the SSO flow
```

`login` does the whole dance and checks the part that is easy to get wrong: the
OAuth flow issues a credential for whichever **organization your browser is
currently in**, not for the account you meant. It tells you which organization
to switch to first, verifies what actually came back against the `org_id` pinned
in your config, refuses to store a mismatch, and puts your previous account back
afterwards.

`claudeswitch add <id>` is the lower-level version: it vaults whatever is live
right now, with the same verification.

Identity is the **organization uuid**, never the email — one email address can
own several organizations with entirely separate quota pools, which is exactly
the case that produced the wrong-account bugs this guard now prevents. Only
`claudeAiOauth` is ever vaulted; your MCP tokens stay with the machine.

## Keeping credentials alive

Refreshing an access token **revokes the previous one**, so a vaulted snapshot
goes stale within hours of that account's session being refreshed elsewhere. The
daemon therefore renews them itself:

```toml
auto_refresh   = true     # keep vaulted credentials alive at all
refresh_window = "1h"     # renew this long before a token expires
refresh_probe  = "24h"    # also refresh each idle account this often; "0s" disables
```

Those are the defaults. It never refreshes the account currently in use — that
would revoke the token your session is holding; Claude Code renews that one
itself and `SyncActive` re-captures it. Refreshing runs in dry-run too: dry-run
means "do not rotate", and letting every stored credential expire while watching
would be a strange reading of it.

`refresh_probe` exists because `/v1/oauth/token` never reports when a *refresh*
token expires. Without a periodic probe a dead one stays invisible until the
moment you need that account; with it, you find out within a day. `cs doctor`
shows the policy and every account's standing.

## Sessions that span accounts

Rotation makes "how much have I used?" a question no single account can answer:
the work continues across swaps, and each account only knows its own share.

```
$ cs session
  Thu 10:51 → 18:51  (8h0m0s)

  ACCOUNT      TOKENS  SHARE  MESSAGES   ACTIVE
  personal      1.20B    56%      4276   5h4m0s
  work-team    947.5M    44%      4115  2h56m0s

  total         2.15B             8391
              ↳ out 1.1M · thinking 131k

  1 switch(es) in this span:
    15:55:30  personal → work-team   active account at 85%, over the 85% trigger
```

It joins the transcripts (per-message token counts and timestamps) with the audit
log (when the active account changed), attributing every message to whichever
account was live when it was written. No network calls, so it costs no quota.
`--since 2h` narrows the window, `--detail` breaks out input/output/cache and models.

## Status line

`claudeswitch statusline` prints one compact line and is strictly read-only —
no polling, no API calls, no state writes — so it is safe to run on every
render. Add to `~/.claude/settings.json`:

```json
"statusLine": { "type": "command", "command": "claudeswitch statusline" }
```

It renders as `personal 56% · 7d 33%`, gains a `!` at the switch threshold and
`!!` past the hard floor, and reads `personal BURNT until 14:30` after a refusal.

## Notifications

The daemon sends a macOS notification when it switches accounts, when every
account is burnt (naming which recovers first), and before an idle account's
refresh token expires — a dead refresh token means that account can no longer
be swapped to *or* polled. Nothing else notifies; `--quiet` disables them.

## Running as a daemon

```sh
./install.sh          # builds, installs, loads a launchd agent in DRY-RUN
./install.sh --live   # ...or let it actually perform swaps
claudeswitch plan     # the current decision, and whether anything will act on it
claudeswitch audit    # what it has decided and done
```

Dry-run is the default and exercises the entire decision path, logging the swap it
*would* make. Run that way for a day first; `claudeswitch audit --kind decision` shows
whether its judgement matches yours.

One daemon per machine, enforced with a lock file. While it runs it owns the
polling and the state file, and the CLI reports its readings instead of making
its own API calls — so checking `status` never costs you API budget or races
the daemon's writes.

## Configuration

`~/.config/claudeswitch/config.toml`:

```toml
switch_at   = 85     # rotate away at this utilization
hard_floor  = 96     # above this, swap mid-turn rather than wait for an idle gap
switch_when = "idle"
cooldown    = "10m"

priority = ["work-a", "work-b", "work-c", "personal"]

[[account]]
id     = "personal"
scope  = "personal"
reserve = 70         # never auto-used above this utilization
org_id = "…"         # pins this entry to a Claude organization
```

`org_id` is worth setting. Without it, a live credential whose organization is
unrecognised is reported as `(live) … unattributed` rather than being filed
under a guess — `status` prints the org id to paste in.

## What it will not do

- **Guess.** An account it cannot read is `unknown`, and `unknown` is never
  treated as available. A percentage is shown only if the API reported it.
- **Estimate quota from token counts.** That was tested against 24 real limit
  hits and produced 40–79% spread. See ground truth, Round 2.
- **Hammer the usage API.** The endpoint allows roughly 5 calls per 5 minutes
  before a 299-second lockout, so the poller holds a budget of 4 and keeps one
  in reserve for verifying a swap.
- **Touch your MCP tokens.** The Keychain item holds Notion and Slack OAuth
  alongside the Claude credential; only the `claudeAiOauth` subtree is ever
  swapped.

## Platforms

| | macOS | Linux |
|---|---|---|
| credentials | Keychain (`security`) | `~/.claude/` files, `0600` in a `0700` dir |
| service | launchd agent | systemd `--user` unit |
| notifications | `osascript` | `notify-send` (absent on servers; harmless) |

Both are exercised by the same test suite. The Linux side was developed against
a real Linux container rather than cross-compiled and hoped for — which caught
two faults a cross-compile would have missed: re-indented JSON breaking the
byte-for-byte mcpOAuth guarantee, and a path check that accepted `..`.

## Layout

```
cmd/claudeswitch      CLI
internal/usage        the /api/oauth/usage adapter + call budget
internal/detector     transcript rejection watcher (safety net)
internal/poller       scheduling within the call budget
internal/state        durable observations (the 7-day window outlives restarts)
internal/keychain     credential read, mcpOAuth-aware
internal/config       declarative accounts
internal/render       the status view
```
