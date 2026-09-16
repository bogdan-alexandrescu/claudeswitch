# claudeswitch

Keeps Claude Code pointed at an account that still has quota.

It observes every Claude account you own, reports on them, switches on command,
and rotates automatically before you hit a wall. Inside Claude Code, a plugin
tells each session where quota stands and lets Claude check, explain and switch
accounts for you.

[`docs/DESIGN.md`](docs/DESIGN.md) records why it is shaped the way it is;
[`docs/GROUND_TRUTH.md`](docs/GROUND_TRUTH.md) holds the measured facts it is
built on.

## Why it exists

Hitting the weekly ceiling blocks work until it resets — days, sometimes. Four
accounts sit idle while one is exhausted. This watches all of them and moves you
onto a fresh one before you notice.

## Install

There are three parts. The binary is required; the daemon and the Claude Code
plugin are each optional, and each needs the binary.

| part | gives you | needs |
|---|---|---|
| binary | every command below | a release download, or Go to build it |
| daemon | automatic rotation, credential renewal, notifications | a source checkout (`install.sh`) |
| Claude Code plugin | quota context in every session, `/claudeswitch:*` skills | the binary |

### From source (binary and daemon)

```sh
git clone https://github.com/bogdan-alexandrescu/claudeswitch
cd claudeswitch
go build -o bin/claudeswitch ./cmd/claudeswitch
./bin/claudeswitch setup
```

`setup` is interactive and does the whole first run. It finds the account you
are already signed into, walks each additional one through a browser that will
actually produce a different account, writes a config with the real seats
pinned, offers to install the daemon in dry-run, and offers to add the status
line to Claude Code.

Run it from the checkout: installing the daemon runs `./install.sh`, which puts
the binary in `~/.local/bin`, adds a `cs` symlink, and loads a launchd agent
(macOS) or a systemd `--user` unit (Linux). See [Running as a
daemon](#running-as-a-daemon).

Pinning cannot be done by hand in advance: you only learn an account's seat uuid
by signing in to it, which is why the config is generated rather than templated.
`init` still writes a bare config if you would rather fill it in yourself, and
`doctor` checks everything that has to be true.

### From a release (binary only)

Each [release](https://github.com/bogdan-alexandrescu/claudeswitch/releases) has archives for
macOS and Linux on amd64 and arm64, and a `checksums.txt`. The archives are
reproducible: the same tag always produces the same bytes.

```sh
VERSION=v0.4.0
TARGET=darwin_arm64            # darwin_amd64, linux_amd64, linux_arm64
curl -LO "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/$VERSION/claudeswitch_${VERSION}_${TARGET}.tar.gz"
curl -LO "https://github.com/bogdan-alexandrescu/claudeswitch/releases/download/$VERSION/checksums.txt"
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf "claudeswitch_${VERSION}_${TARGET}.tar.gz"
install -m 0755 claudeswitch ~/.local/bin/claudeswitch
ln -sf claudeswitch ~/.local/bin/cs
claudeswitch setup
```

The archive holds the binary, `LICENSE` and this README. It does not include
`install.sh`, so for the daemon use a source checkout.

`cs` is a symlink rather than a shell alias deliberately: aliases do not exist
in non-interactive shells, scripts, or Claude Code's `!` prefix.

### In Claude Code (plugin)

Install the binary first. Then, inside Claude Code:

```
/plugin marketplace add https://github.com/bogdan-alexandrescu/claudeswitch
/plugin install claudeswitch@claudeswitch
```

or from a shell:

```sh
claude plugin marketplace add https://github.com/bogdan-alexandrescu/claudeswitch
claude plugin install claudeswitch@claudeswitch
```

Start a new session for it to take effect, then run `/claudeswitch:setup`: it
checks the binary is reachable, adds the status line, and runs `doctor`. The
status line can also be added directly:

```sh
claudeswitch statusline install
```

The plugin's version always matches the binary release it shipped with. To
update it:

```sh
claude plugin marketplace update claudeswitch
claude plugin update claudeswitch@claudeswitch
```

To remove it: `claude plugin uninstall claudeswitch@claudeswitch`, and
`claudeswitch statusline uninstall` for the status line.

## Use

Every command also works as `cs <command>`.

**Looking**

```sh
claudeswitch status              # every account's utilization, both windows
claudeswitch status --detail     # ...with reading age, burn rate, binding limit
claudeswitch top                 # the same, redrawn in place (ctrl-c to leave)
claudeswitch whoami              # which Claude account is live right now
claudeswitch accounts            # what is in the vault
claudeswitch session             # token usage across every account used in a span
claudeswitch history -days 21    # deduped rejection history from your transcripts
```

**Deciding**

```sh
claudeswitch plan                # the rotation decision right now (changes nothing)
claudeswitch why                 # the same decision, account by account, with reasons
claudeswitch audit               # what the daemon observed, decided and did
claudeswitch audit --kind switch --since 24h
```

**Acting**

```sh
claudeswitch use <id>            # swap onto a vaulted account (hot; no restart)
claudeswitch login <id> --direct # sign in to an account and vault it
claudeswitch add <id>            # vault the credential that is live right now
claudeswitch refresh <id>        # renew a vaulted credential (never the live one)
```

**Managing**

```sh
claudeswitch setup               # guided first run
claudeswitch config              # every setting in force; `config <name> <value>` changes one
claudeswitch doctor              # config, vault, keychain, usage API, daemon, Claude Code
claudeswitch identify            # record which seat each vaulted credential belongs to
claudeswitch rename <old> <new>  # re-file a vaulted account under another id
claudeswitch forget <id>         # drop an account's recorded observations
claudeswitch remove <id>         # delete an account's vault entry and observations
claudeswitch watch [--live]      # run the daemon in the foreground
claudeswitch uninstall           # stop the daemon and remove what was installed
```

**Claude Code**

```sh
claudeswitch statusline          # one line for Claude Code's status line (read-only)
claudeswitch statusline install  # add it to ~/.claude/settings.json
claudeswitch context             # the quota summary the plugin gives each session
```

Most read commands take `--json`.

`status` on a machine with one account configured:

```
  ACCOUNT      5-HOUR                          7-DAY                           RESETS    STATE
  personal     [#########.............]  40.0% [#######...............]  30.0% 3h10m     ACTIVE available
                 ↳ binding: session (normal) 40%, resets 3h10m

  thresholds  switch ≥85% session / ≥98% weekly   hard floor ≥99%   swap idle, forced after 30s
  api budget  11 scheduled call(s) available now (12 per 5m0s, one held for swaps)
```

## Adding an account

```sh
claudeswitch login work-a --direct                     # sign in, verify, vault
claudeswitch login work-a --direct --browser Safari    # ...in a browser signed into that account
claudeswitch login work-a --code <code>                # finish, with the code the browser shows
claudeswitch login work-a --sso                        # an SSO-backed organization
```

A login returns a credential for **whichever account your browser is signed
into**; nothing in the request can override that. So sign in using a browser
that holds the account you want — separate browser applications keep separate
cookies, which is what `--browser` is for.

`login` checks the part that is easy to get wrong. It verifies the credential
that came back against the seat pinned in your config, refuses to store a
mismatch, and names the account it actually got. `--direct` obtains the
credential without touching your live session at all; without it, `login` signs
in through Claude Code and puts your previous account back afterwards.

`claudeswitch add <id>` is the lower-level version: it vaults whatever is live
right now, with the same verification.

Identity is the **seat** — one person within one organization — never the
email. One address can belong to several organizations with separate quota
pools, and one team organization holds a separate pool for each member. Only
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
shows the policy and every account's standing, and `status` says when an
account cannot be renewed.

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

## Inside Claude Code

With the [plugin installed](#in-claude-code-plugin), three things change.

### Every session starts knowing where quota stands

```
[claudeswitch] active personal · session 24% (resets 3h47m) · week 44% (resets 4d15h)
[claudeswitch] rotates at session 85% / week 98%, mid-turn at 99% · daemon running, rotates automatically
```

That is `claudeswitch context`, run by a SessionStart hook. It reads saved state
only — no keychain, no API calls — and never fails a session start. Two lines is
the normal case; anything more needs attention:

| extra | means |
|---|---|
| `· reading 4m old` | the latest reading is older than three poll intervals |
| `personal was refused until 14:30` | the account hit a limit |
| `daemon NOT running` / `daemon in dry-run` | nothing will rotate automatically |
| `next: switch to work-a …` | a switch is due; the line gives the `use` command |
| `next: wait …` | every account is out; says which recovers first |
| `not on PATH` / `older than this plugin` | install or update the binary |

### Skills

Invoke them by name, or just ask — Claude picks the matching skill.

| skill | ask something like | changes anything |
|---|---|---|
| `/claudeswitch:status` | "how much quota is left?" | no |
| `/claudeswitch:why` | "why didn't it switch?" | no |
| `/claudeswitch:session` | "how much have I used today?" | no |
| `/claudeswitch:doctor` | "claudeswitch isn't polling" | no |
| `/claudeswitch:switch` | "move me to the account with most room" | yes, without asking: a swap is hot and reversible |
| `/claudeswitch:login` | "add my work account" | yes, after confirming account and browser |
| `/claudeswitch:setup` | "set up claudeswitch" | settings.json; asks before replacing a status line |

A switch made from a skill takes effect from Claude's next request, in the
current session included. First-time account setup stays in a terminal
(`claudeswitch setup`): it is interactive in ways a skill cannot be.

### Status line

`claudeswitch statusline` prints one compact line and is strictly read-only —
no polling, no API calls, no state writes — so it is safe to run on every
render:

```
personal  session ▓▓░░░░░░░░ 24% 3h48m  ▸week ▓▓▓▓░░░░░░ 44% 4d15h
```

`▸` marks the window that binds, and an arrow after its figure shows which way
it is moving. Bars turn yellow at 60%, and at the switch threshold and hard
floor take the colours for "rotation coming" and "mid-turn swap". Past the
threshold the line says what happens next — `· rotating to work-a`,
`· all out, quota at 14:30`, `· holding` — and a refused account reads
`refused until 14:30`. `· read 12m ago` means the readings have stopped moving.

`claudeswitch statusline install` writes it to `~/.claude/settings.json`
(honouring `CLAUDE_CONFIG_DIR`). It leaves an existing status line alone unless
you pass `--force`, keeps the previous file as `settings.json.claudeswitch.bak`,
and preserves the order of your keys. `uninstall` removes it only if it is
claudeswitch's. By hand, it is:

```json
"statusLine": { "type": "command", "command": "claudeswitch statusline" }
```

## Notifications

The daemon sends a desktop notification when it switches accounts, when every
account is burnt (naming which recovers first), and before an idle account's
refresh token expires — a dead refresh token means that account can no longer
be swapped to *or* polled. Nothing else notifies; `--quiet` disables them.

## Running as a daemon

```sh
./install.sh          # builds, installs, loads the service in DRY-RUN
./install.sh --live   # ...or let it actually perform swaps
claudeswitch plan     # the current decision, and whether anything will act on it
claudeswitch audit    # what it has decided and done
```

Dry-run is the default and exercises the entire decision path, logging the swap it
*would* make. Run that way for a day first; `claudeswitch audit --kind decision` shows
whether its judgement matches yours. **Re-running `./install.sh` without `--live`
puts a live daemon back into dry-run.**

On macOS a rebuilt binary is a new program to the Keychain, so `install.sh`
triggers the access prompt while you are at the keyboard — click **Always
Allow**. A daemon cannot answer that prompt and would otherwise sit silently.

One daemon per machine, enforced with a lock file. While it runs it owns the
polling and the state file, and the CLI reports its readings instead of making
its own API calls — so checking `status` never costs you API budget or races
the daemon's writes. Its log is `~/.local/state/claudeswitch/daemon.log`.

## Configuration

`~/.config/claudeswitch/config.toml`, written by `setup`:

```toml
switch_at        = 85     # rotate away at this session (5-hour) utilization
switch_at_weekly = 98     # ...and at this weekly utilization
hard_floor       = 99     # above this, swap mid-turn rather than wait for an idle gap
switch_when      = "idle"
cooldown         = "10m"
max_switch_wait  = "30s"  # how long a due switch waits for an idle gap

priority = ["work-a", "work-b", "personal"]

[[account]]
id           = "personal"
scope        = "personal"
reserve      = 70         # never auto-used above this utilization
account_uuid = "…"        # the seat: this person...
org_id       = "…"        # ...in this organization
```

The two triggers differ on purpose: 85% of a 5-hour window is nearly gone and
refills the same afternoon, while 85% of a weekly one still holds days of work.

`cs config` lists every setting with its value, and `cs config <name> <value>`
changes one with validation. A live credential that matches no pinned seat is
reported as `ACTIVE, unattributed` rather than filed under a guess.

## What it will not do

- **Guess.** An account it cannot read is `unknown`, and `unknown` is never
  treated as available. A percentage is shown only if the API reported it.
- **Estimate quota from token counts.** That was tested against 24 real limit
  hits and produced 40–79% spread. See ground truth, Round 2.
- **Hammer the usage API.** The endpoint locks a caller out for 299 seconds after
  a burst. Every claudeswitch process shares one budget of 12 calls per 5
  minutes, with one always held back to verify a swap.
- **Touch your MCP tokens.** The Keychain item holds Notion and Slack OAuth
  alongside the Claude credential; only the `claudeAiOauth` subtree is ever
  swapped.
- **Prompt for the Keychain from Claude Code.** The status line and the session
  hook read saved state only.

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
internal/poller       scheduling within the call budget
internal/policy       whether to rotate, and where to
internal/vault        per-account credentials, and the swap
internal/credstore    where the platform keeps credentials
internal/keychain     credential read, mcpOAuth-aware
internal/oauth        login and token refresh
internal/detector     transcript rejection watcher (safety net)
internal/state        durable observations (the 7-day window outlives restarts)
internal/audit        what was observed, decided and done
internal/session      work that spans several accounts
internal/config       declarative accounts
internal/render       the status view
internal/notify       desktop notifications
plugin/               the Claude Code plugin: SessionStart hook and skills
.claude-plugin/       the marketplace entry that lists the plugin
```
