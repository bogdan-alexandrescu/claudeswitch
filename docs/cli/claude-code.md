# cs and Claude Code

The commands Claude Code itself runs (the status line and the session-start context), the usage and history reports, and the plugin's `/cs` commands.

See also: [cs status](status.md) · [cs chrome](chrome.md) · [setup](setup.md) · [GUIDE → Inside Claude Code](../GUIDE.md#inside-claude-code) · [GUIDE → Sessions that span accounts](../GUIDE.md#sessions-that-span-accounts)

## Synopsis

```
cs statusline                  [--config PATH]
cs statusline install          [--force]
cs statusline uninstall
cs context                     [--config PATH]
cs session                     [--since DURATION] [--profile NAME] [--detail] [--json] [--config PATH]
cs history                     [--days N]
cs history --usage             [--days N] [--json] [--config PATH]
cs audit                       [-n N] [--kind KIND] [--since DURATION]
```

Flags may come before or after the arguments. An unknown flag prints the
command's flag list and exits 2.

## cs statusline

One compact line for Claude Code's status line:

```
personal  session ▓▓░░░░░░░░ 24% 3h48m  ▸week ▓▓▓▓░░░░░░ 44% 4d15h
```

It is strictly read-only: no polling, no API calls, no keychain, no state
writes, so it is safe to run on every render. It shows the profile of the
session it runs in (found from that session's `CLAUDE_CONFIG_DIR`), and with
several profiles it starts with the profile's name, as in `work: work-team`.

- `▸` marks the window that binds. An arrow (`↑` or `↓`) after its figure
  shows which way it moved since the reading before.
- Each figure is coloured by band: below 60%, from 60%, from the profile's
  `switch_at`, and from its `hard_floor`.
- A per-model weekly limit appears only when it binds or is at the weekly
  trigger.
- A refused account reads `personal  refused until 14:30  week ...`.
- After a switch is due the line says what happens next, and
  `· read 12m ago` means the readings have stopped moving.
- `· login lab3 ≠` means Claude Code in this profile is logged in as
  another account than the one it uses, so Claude in Chrome will not connect.
  Every swap lines the two up, and the daemon puts a stray one right on its
  next poll; `cs whoami --sync` does it now.
- `⚠ daemon outdated` at the end means the running daemon is older than this
  binary: restart it.

Colour is on unless `NO_COLOR` is set (to anything) or
`CLAUDESWITCH_NO_COLOR` is non-empty.

| flag | default | meaning |
|---|---|---|
| `--config PATH` | `~/.config/claudeswitch/config.toml` | the config file |

It never fails a prompt: an unreadable config prints nothing, and other
problems print a short `claudeswitch  ...` note (`no account selected`,
`no reading yet`, `this session's CLAUDE_CONFIG_DIR is no configured profile`).
Exit status is 0 in all those cases.

The bands, trend arrows and every message in detail:
[GUIDE → Status line](../GUIDE.md#status-line).

### cs statusline install / uninstall

`install` writes `"statusLine": {"type": "command", "command": "claudeswitch statusline"}`
to Claude Code's `settings.json` (`~/.claude/settings.json`, or the one in
`CLAUDE_CONFIG_DIR` when that is set). It keeps the order of your keys and
leaves a copy of the previous file as `settings.json.claudeswitch.bak`.

```
$ cs statusline install
  ✓ status line set in ~/.claude/settings.json (takes effect on the next render)
```

| flag | default | meaning |
|---|---|---|
| `--force` | off | `install` only: replace a status line that is not claudeswitch's |

Without `--force`, someone else's status line is left alone and `install`
fails, exit 1, with `... already has a status line (...); leaving it`. A
status line that already runs `claudeswitch statusline` or `cs statusline`
is reported as `nothing to do`.

`uninstall` removes the status line only if it is claudeswitch's; otherwise it
says `status line is not claudeswitch's (...); leaving it` and exits 0.

`cs setup` and `/cs setup` offer the same install.

## cs context

The lines the plugin's SessionStart hook hands Claude at the start of every
session:

```
[claudeswitch] active personal · session 24% (resets 3h47m) · week 44% (resets 4d15h)
[claudeswitch] rotates at session 85% / week 98%, mid-turn at 99% · daemon running, rotates automatically
```

Like the status line it is read-only (no keychain, no API, no state writes)
and describes the session's own profile; with several profiles each line
starts `[claudeswitch] profile work · `. Two lines is the normal case. Extra
lines, all from the source:

| line | means |
|---|---|
| `· reading 12m old` (appended) | no reading newer than 10 minutes |
| `personal was refused until 14:30` | the live account hit a limit |
| `daemon NOT running, so nothing rotates automatically` | in place of `daemon running, ...` |
| `daemon in dry-run: it reports swaps but does not make them` | likewise |
| `next: <decision>. \`claudeswitch use <id>\` swaps now, no restart` | a switch is due |
| `next: <decision>` | every account is out; it says which recovers first |
| `the running daemon is older than this claudeswitch, ...` | restart the daemon |
| `no accounts set up yet; run \`claudeswitch setup\` in a terminal` | nothing configured |
| `config unreadable; run \`claudeswitch doctor\` in a terminal` | the config failed to load |

| flag | default | meaning |
|---|---|---|
| `--config PATH` | `~/.config/claudeswitch/config.toml` | the config file |

It always exits 0, so it can never fail a session start.
[GUIDE → Every session starts knowing where quota stands](../GUIDE.md#every-session-starts-knowing-where-quota-stands).

## cs session

Adds up token usage for a span of work across every account that served it.
Rotation splits one stretch of work over several accounts, and each account
only knows its own share, so this is the one place the total exists. It reads
Claude Code's transcripts, in every profile's directory.

```sh
cs session
```

<p align="center"><img src="../images/cli-session.svg" width="720" alt="Output of cs session: eight hours split between research and personal, with tokens, share, messages and active time, and the one switch in the span"></p>

The span, then one row per account (tokens, share of the total, messages,
active time), `(unattributed)` for usage it cannot place, the total, output
and thinking tokens, and the switches in the span with their reasons
(`no switches in this span` when there were none).

It covers every profile (decided 2026-10-08): each message counts against
the account live in its own profile when it was written, so an account's
row adds up its work in whichever profile used it, and the switch list
merges every profile's switches, each labelled with its profile when there
are several. `--profile NAME` reports one profile only.

| flag | default | meaning |
|---|---|---|
| `--since DURATION` | see below | how far back to look, e.g. `2h`, `24h` |
| `--profile NAME` | every profile | report only this profile's work and switches |
| `--detail` | off | per account: input, output and cache tokens, and messages per model |
| `--json` | off | machine-readable output |
| `--config PATH` | `~/.config/claudeswitch/config.toml` | the config file |

Without `--since` the span starts at today's first switch in any profile
reported, or 8 hours ago if that is earlier. So a day of work across
rotations reads as one session, and a quiet day is the last 8 hours.
"Today" is your local calendar day; `--profile` limits the switches it
looks at to that profile.

`--json`:

```
{"from": ..., "to": ..., "profiles": ["default", ...],
  "accounts": [{"account", "tokens", "messages", "active_seconds",
  "input", "output", "cache_read", "cache_write"}], "total_tokens": ..., "messages": ...,
  "switches": <count>,
  "switch_events": [{"at", "profile", "from", "to", "reason"}]}
```

A transcript problem is printed as `note: ...` on stderr and the report still
runs. Exit 0, or 1 if the config or state cannot be loaded.
[GUIDE → Sessions that span accounts](../GUIDE.md#sessions-that-span-accounts).

## cs history

Reads Claude Code's transcripts for limit refusals, de-duplicates them, and
lists the real ones, newest first:

```
  412 raw records over 21 days → 3 real limit hits after dedupe

  WHEN         WINDOW   CLEARED
  ...
```

Each row is the day, the window (`5-hour` or `weekly`) and the time it
cleared. With none: `no refusals recorded`. (The header line's wording is the
source's; its numbers here are illustrative.)

| flag | default | meaning |
|---|---|---|
| `--days N` | `21` | how far back to read |

Exit 0, or 1 if the transcripts cannot be read.

### cs history --usage

Each account's session and weekly utilization over the last `--days`
(default 30), from the daemon's readings log, and the switches from its
audit log (IMPROVEMENTS F7). It reads those two files and the config, and
writes nothing; no keychain, no API.

```
  usage over the last 30 day(s), from the daemon's readings log

  work-1             412 readings · peak 5h 91% · 7d 64% · last Thu 09 Oct 11:58
  work-2             no readings

  7 switches; `cs history --usage --json` has every reading and switch
```

`--json`:

```json
{"days": 30, "from": "2026-09-09T12:00:00Z", "to": "2026-10-09T12:00:00Z",
 "accounts": [
  {"id": "work-1", "profile": "work", "pool": "work", "profiles": ["work"], "configured": true,
   "series": [{"at": "2026-10-09T11:58:02Z", "five_hour": 42, "seven_day": 61.5}, …]},
  {"id": "work-2", "profile": "work", "pool": "work", "profiles": ["work"], "configured": true,
   "series": []}],
 "switches": [
  {"at": "2026-10-09T10:01:00Z", "profile": "work", "from": "work-1", "to": "work-2",
   "reason": "active account at 86%, over the 85% session trigger", "forced": false}]}
```

- `accounts`: the configured ones in rotation order (disabled ones after),
  then any the log names that the config no longer does (`configured`
  `false`, `profile` and `pool` `null`, `profiles` empty), by name. `pool`
  is the pool the account is in (`null` for none), `profiles` every profile
  that may use it, and `profile` the first of them.
- `series`: oldest first. `five_hour` / `seven_day` are percentages, `null`
  when the reading did not report that window (never `0`).
- `switches`: every `switch` in the audit log in the span, oldest first —
  the daemon's, `cs use`'s and a profile seed's. `from` is `null` when none
  was recorded.
- `--json` without `--usage`, and `--days` under 1, are `usage` errors.

The readings log (`~/.local/state/claudeswitch/readings.jsonl`) is written
by the daemon only: one line per new reading of each account, kept 30
days (the last day whole, older days thinned to one reading per account per
15 minutes) and under 4 MiB. Its format is in
[GUIDE → Usage history](../GUIDE.md#usage-history). With no daemon running,
there is nothing new to show.

## cs audit

What the daemon observed, decided and did, newest first, from its audit log:

```
  WHEN         WHAT       DETAIL
  10-08 15:01  switch     research → personal  active account at 86%, over the 85% …
  10-08 15:01  decision   switch → personal  ...
```

Kinds and what their DETAIL shows:

| kind | shown as | detail |
|---|---|---|
| `switch` | switch | `from → to` and the reason |
| `rejection` | refused | the account, the window, when it cleared |
| `decision` | decision | stay, switch or wait, the target, the reason |
| `unpin` | unpin | the pin the safety valve lifted, and why (`pin on work-1 lifted: it was refused`) |
| `severity` | severity | an account's limit changing severity, with the percentage |
| `error` | error | the error |

`[dry-run]` marks what a dry-run daemon would have done; `[forced]` a swap
made mid-turn. With an empty log: `no audit events yet (run \`claudeswitch watch\`)`.

| flag | default | meaning |
|---|---|---|
| `-n N` | `30` | how many events |
| `--kind KIND` | all | only `decision`, `switch`, `rejection`, `unpin`, `severity` or `error` |
| `--since DURATION` | all | only events newer than this, e.g. `24h` |

`cs audit --kind decision` is how to check a dry-run daemon's judgement before
`cs daemon live` ([cs daemon](daemon.md)). Exit 0, or 1 if the log cannot be
read. The rows above show the layout; their values are illustrative.

## The plugin

The Claude Code plugin `cs@claudeswitch` (installing it:
[README → Claude Code plugin](../../README.md#claude-code-plugin)) adds four
hooks and the `/cs` command. It carries no logic of its own: each piece calls
the binary, found on `PATH` or at `~/.local/bin/claudeswitch`.

### Hooks

| hook | runs | what it does |
|---|---|---|
| `SessionStart` | `cs context` | the lines above. Without the binary it says `plugin installed but \`claudeswitch\` is not on PATH`; with one too old for `context`, `the installed binary is older than this plugin`. Always exits 0 |
| `PostToolUseFailure` on `mcp__claude-in-chrome__*` | `cs chrome hint` | when a browser tool failed because the extension is on another claude.ai account or not connected, opens the live account's own Chrome profile and tells Claude to retry, never to ask for a sign-in, once per rotation. Only lines starting `[claudeswitch]` reach Claude ([cs chrome](chrome.md#cs-chrome-hint-plugin-hook)) |
| `PreToolUse` on `Artifact`, `ArtifactComments`, `ArtifactData`, `mcp__claude_ai_Claude_Docs__*` and `mcp__claude-in-chrome__*` | `cs hook pre` | **settles**, then **guards** ([below](#claudeai-calls-across-a-switch)). It never answers a permission prompt: a call it lets through goes on as it would without the plugin |
| `PostToolUse` on `Artifact` | `cs hook post` | records which account published an artifact, for the guard |

`cs hook pre` times out after 330 seconds, past the longest settle (5
minutes); the others after 10. Every hook exits 0: none is ever why a tool
call or a session fails.

### claude.ai calls across a switch

After a switch, Claude Code's claude.ai tools (Claude in Chrome, Artifact,
Claude Docs) go on acting as the previous account for up to about half a
minute: measured at up to 35 seconds over four switches. And claude.ai keeps
an artifact for the account that published it: only that account can read,
change or delete it. So the plugin does two things around those calls.

**Settle.** Before such a call, `cs hook pre` waits until
`settle_after_swap` (45 seconds; `0s` turns it off) after the session's
profile last switched, and again if another switch comes while it waits, so
the call acts as the account now live. A call made long after a switch does
not wait at all.

**Guard.** After a publish, `cs hook post` records the artifact's id, the
file it came from and the session, for the account live then (and its seat,
so a renamed account still owns its artifacts). If a switch came while the
call ran, the owner is uncertain and nothing is recorded. Before an
Artifact, ArtifactComments or ArtifactData call on a recorded artifact
(named by its URL, or, in the session that published it, by its file) while
another account is live, `cs hook pre` refuses the call, and Claude reads:

```
[claudeswitch] This artifact belongs to work-a: it was published while work-a was live, and
claude.ai lets only the account that published an artifact read, change or delete it. This
session now uses work-b (claudeswitch rotated its profile). Either run `cs use work-a` (work-a
has room: session 40%, week 20%) and retry, or publish it again as a new artifact under work-b.
```

When the owner has no room it offers only the new artifact. `open` is never
refused (it shows the page in the person's own browser), nor is an artifact
the plugin did not see published.

**In Chrome too.** claude.ai shows an artifact only to its owner, so Claude
in Chrome opening another account's gets "This artifact is private". When the
live account has a Chrome profile of its own (`cs chrome add`, mapped to no
other account), the browser Claude in Chrome reaches is signed in as the live
account, and a `navigate` (or a `browser_batch` navigate) to a recorded
artifact of another account is refused the same way, offering `cs use
<owner>` or opening one of the live account's own. With a Chrome profile
shared by several accounts, or none, nothing is refused: whose claude.ai it
shows is not known. The record is
`~/.local/state/claudeswitch/artifacts.json`, the newest 500.

After a switch, claudeswitch opens the new account's own Chrome profile in
the background, so Claude in Chrome finds its browser (C4). A session already
connected keeps its browser; one that started on another account and lost its
connection moves to the live account's browser with `/mcp` → claude-in-chrome
→ Reconnect (`/chrome` does not do it). Without the person, the dependable
route is to run each browser step in a fresh process, `echo '<the whole browser
task>' | claude -p --chrome --allowedTools mcp__claude-in-chrome`: a new process
always reaches the live account's browser (a deck session finished its Google
Slides edits this way, 2026-10-23). `cs chrome check` shows what such a process
finds.

Rotation itself waits for claude.ai work too: `hold_for_claude_ai`
([GUIDE → Configuration](../GUIDE.md#configuration)).

### /cs

`/cs <command> [args]` works like `cs` in a terminal. With no command it is
`status`. An unknown command lists the commands. You can also just ask, and
Claude picks the matching skill.

| command | skill | what it runs | changes anything |
|---|---|---|---|
| `/cs status` | `cs:status` | `cs status` | no |
| `/cs why` | `cs:why` | `cs why` | no |
| `/cs session` | `cs:session` | `cs session` | no |
| `/cs doctor` | `cs:doctor` | `cs doctor` | no |
| `/cs switch <id>`, `/cs use <id>` | `cs:switch` | `cs why --json`, then `cs use <id>` | yes, without asking: a swap is hot and reversible |
| `/cs login <id>`, `/cs add <id>` | `cs:login` | `cs login <id> --direct` | yes, after confirming the account and browser |
| `/cs pool` | `cs:pool` | `cs pool list`, then `cs pool …` or `cs profile pools` | the config, after confirming; listing changes nothing |
| `/cs setup` | `cs:setup` | checks the binary, `cs statusline install`, `cs doctor` | `settings.json`; asks before replacing a status line |

The commands live under `/cs` because `/status`, `/login` and `/doctor` belong
to Claude Code. A switch made from `/cs switch` takes effect from Claude's
next request, in the current session too. First-time account setup stays in
a terminal (`cs setup`).
[GUIDE → Skills](../GUIDE.md#skills).

[← Documentation index](../README.md)
