# cs status, top, why, plan, whoami

The read commands: where every account stands, what rotation will do and why, and which account is live.

See also: [accounts](accounts.md) · [profiles](profiles.md) · [daemon](daemon.md) · [concepts](../concepts.md) · [app: popover](../app/popover.md) · [GUIDE → Commands](../GUIDE.md#commands)

Every command here also works as `claudeswitch <command>`. None of them
changes the config. `status` and `plan` may take a fresh usage reading when
no daemon is keeping the readings current; the others only read saved state.

Flags can come before or after the positional arguments. Every flag is
written `--name` or `-name`.

## Exit status

The same for every command on this page:

| status | when |
|---|---|
| `0` | success |
| `1` | the command failed; the message is on stderr as `claudeswitch: <message>`. With `--json` the error is one object on stdout instead: `{"error": {"code", "message", "hint"}}` ([error codes](../APP_CLI.md#error-codes)) |
| `2` | an unknown command, no command, or a flag the command does not know (Go's flag package prints the usage) |

`--profile NAME` with a name that is not a configured profile fails with
status 1.

## cs status

```
cs status [--profile NAME] [--detail] [--json] [--refresh=false] [--max-age DURATION] [--config PATH] [-v]
```

Shows every profile and every account: session (5-hour) and weekly
utilization, when each window clears, each account's state, the recent
switches, and the call budget.

| flag | default | meaning |
|---|---|---|
| `--profile NAME` | every profile | show only this profile |
| `--detail` | off | per-account detail: bars, reading age, burn rate, binding limit |
| `--json` | off | machine-readable output |
| `--refresh` | `true` | take a fresh reading of any account whose reading is too old. Pass `--refresh=false` to read saved state only |
| `--max-age DURATION` | three poll intervals (`3 × poll_active`, so 9m by default) | how old a reading may be before `--refresh` re-reads it |
| `--config PATH` | `~/.config/claudeswitch/config.toml` | the config to read |
| `-v` | off | verbose logging, on stderr |

When a daemon is running and its readings are fresh, `status` reads nothing
itself: it reports the daemon's readings, and its footer says so. It steps in
only when the daemon has plainly fallen behind. The budget it spends is the
same one the daemon uses, so it cannot overspend
([→ call budget](../concepts.md)).

<p align="center"><img src="../images/cli-status.svg" width="720" alt="Output of cs status: the default profile on personal at 44% of its weekly window with research refused until its session resets, and the work profile on work-1 at 87% of its 5-hour window, rotating to work-team"></p>

Each profile block shows:

- the profile, its thresholds (`switch ≥85% / ≥98%`), and its
  `CLAUDE_CONFIG_DIR`;
- its pool and hard floor;
- the live account, its binding window, and how many points are left before
  it rotates, or what happens next (`rotating to work-team`);
- one row per account. `▸` marks the live one. **5H** and **7D** are the two
  windows, with a dot bar and a larger dot at the threshold. **CLEARS** is
  when the binding window resets. **STATE** is the account's state
  (`available`, `refused · five_hour`, `no headroom · 5h`, and so on);
- **RECENT SWITCHES**, the last five, with the reason for each.

The footer says where the readings came from, the idle-swap setting, how many
API calls are spare, and the legend: `!` means flagged by the API, `~` means
projected from an older reading.

On a terminal the output starts with the `◎ claudeswitch` header (version,
daemon, last poll); piped, it does not. `NO_COLOR` turns colour off and
`CLICOLOR_FORCE` turns it on when piped.

With `--json` the output is one object: the default profile's `active`
account, `accounts` (each with `id`, `vaulted`, `active`, `state`,
`five_hour`, `seven_day`, `binding_window`, `clears_at`, `read_at`,
`burn_per_min` and more), the `thresholds`, `daemon_running`,
`api_calls_spare` and `degraded`. The app reads `account list --json` and `why --json` instead
([accounts](accounts.md#cs-account-list), [why](#cs-why)).

Exit status: as above. A config that fails to load is a failure; a missing
config file is not (it shows what it can).

## cs top

```
cs top [--profile NAME] [--every DURATION] [--config PATH]
```

The `status` view, redrawn in place, until you press ctrl-c. It reads saved
state only and never takes a reading itself.

| flag | default | meaning |
|---|---|---|
| `--profile NAME` | every profile | show only this profile |
| `--every DURATION` | `2s` | how often to redraw; anything under `1s` is raised to `1s` |
| `--config PATH` | the default config | the config to read |

It draws to a terminal (alternate screen, hidden cursor, both restored on
exit). Piped, it fails with "``cs top` draws to a terminal; use `cs status`
when piping``" and status 1. The last line reads
`refreshed HH:MM:SS · every 2s · ctrl-c to leave`.

## cs why

```
cs why [--profile NAME] [--json] [--config PATH]
```

The rotation decision for each profile, account by account, with the
reasoning. It reads saved state only and changes nothing.

| flag | default | meaning |
|---|---|---|
| `--profile NAME` | every profile | explain only this profile |
| `--json` | off | machine-readable output |
| `--config PATH` | the default config | the config to read |

<p align="center"><img src="../images/cli-why.svg" width="720" alt="Output of cs why: default is staying put on personal; work is rotating to work-team because work-1 is over the 85% session trigger; with weekly pace for each account"></p>

Each profile block gives:

- the decision (`staying put`, `rotating to work-team`, waiting) and its
  reason;
- **CONSIDERED, in order**: every account in the pool in rotation order,
  `▸` for the live one, `✓` for an eligible target, `✗` for one that cannot
  be used, with why (`refused on its five_hour window · clears in 37m`,
  `at 88%, over the 85% session trigger`);
- **WEEKLY, at the pace so far**: each account's weekly use against how much
  of the week has gone, and whether it is on pace to use it all or how much
  would expire unused.

`--json` gives one object per profile, with `decision` (`kind`, `reason`,
and `target` for a switch), `accounts` (each with `eligible`, `utilization`,
`window`, `why`, `clears_at`, `weekly_pace` and `model_limits`), `best` and
`thresholds`. The full shape is in
[APP_CLI.md → why](../APP_CLI.md#why).

Exit status: as above.

## cs plan

```
cs plan [--profile NAME] [--json] [--refresh=false] [--config PATH]
```

The decision alone, without the per-account reasoning, and whether anything
will act on it. Changes nothing.

| flag | default | meaning |
|---|---|---|
| `--profile NAME` | every profile | plan only this profile |
| `--json` | off | machine-readable output |
| `--refresh` | `true` | when no daemon is running, read each profile's live account first. Pass `--refresh=false` to use saved state |
| `--config PATH` | the default config | the config to read |

After the decision, `plan` says what will happen to it, in one of these
lines (verbatim from the source):

```
  no daemon is running, so nothing will act on this.
  a LIVE daemon is running: it will act on a decision like this.
  a dry-run daemon is running: it will log this decision and change nothing.
```

`--json` gives `{"decision": {...}}` with no `[[profile]]` blocks, or
`{"profiles": [{"profile": "default", "decision": {...}}, ...]}` with them.
A decision has `kind` and `reason`, and may have `target`, `forced` (a
mid-turn swap past the hard floor), `failover`, `recovers_account` and
`recovers_at`.

Exit status: as above.

## cs whoami

```
cs whoami [--profile NAME] [--config PATH]
```

Which Claude account the live credential belongs to, asked of the usage API
rather than Claude Code's cached account block (which does not follow a
swap). Without `--profile`, it is the profile this shell's
`CLAUDE_CONFIG_DIR` belongs to
([→ which profile a command acts on](../GUIDE.md#which-profile-a-command-acts-on)).
It works even when the config does not load.

| flag | default | meaning |
|---|---|---|
| `--profile NAME` | this shell's profile | the profile whose live credential to check |
| `--config PATH` | the default config | the config to read |

It reads the live credential, so on macOS it is also the command to run once
after installing a new binary: it raises the Keychain prompt while you are
there to click **Always Allow** ([README → Daemon](../../README.md#daemon)).

The output's lines, as the source prints them:

```
  profile      <name>                    (with several profiles)
  signed in as  <email> (<name>)
  account uuid  <uuid>   <- this is the quota pool, not the org
  organization  <org name>  <short org id>
  live credential belongs to organization <org id>
  Claude Code shows  <email> / <org name>
  → known to claudeswitch as "personal".
```

If Claude Code's cached account disagrees with the credential, it says so
with a `⚠` line. If the usage API is rate limited it says "cannot confirm
right now" and prints only the cached view, with a warning not to vault
anything on its word. The last line names the configured account the seat
matches, or says none does.

There is no `--json` form.

Exit status: 1 when the live credential cannot be read or the profile cannot
be resolved; 0 otherwise, including when the API cannot confirm the account.

[← Documentation index](../README.md)
