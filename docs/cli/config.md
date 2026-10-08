# cs config

Read and change claudeswitch's settings, with validation, and list every setting with its default and range.

See also: [Concepts](../concepts.md) · [Settings → Rotation](../app/settings-rotation.md) · [Settings → Polling](../app/settings-polling.md) · [Settings → Advanced](../app/settings-advanced.md) · [cs profile set](profiles.md#profile-set) · [GUIDE → Configuration](../GUIDE.md#configuration) · [APP_CLI → config](../APP_CLI.md#config)

The config is `~/.config/claudeswitch/config.toml`, written by `cs setup`,
`login`, `add` and the commands on this page. A running daemon picks up every
edit without a restart.

```
claudeswitch config [--json]
claudeswitch config <name> [--profile P] [--json]
claudeswitch config <name> <value> [--json]
claudeswitch config get <name> [--profile P] [--json]
claudeswitch config set <name> <value> [--json]
claudeswitch config schema [--json]
claudeswitch config clean [--yes] [--json]
```

| flag | meaning | default |
|---|---|---|
| `--config PATH` | the config file to read and edit | `~/.config/claudeswitch/config.toml` |
| `--json` | one JSON object on stdout, never a prompt | off |
| `--profile P` | `get`: the value in force in profile P | none (the global value) |
| `--yes` | `clean`: do not ask for confirmation | off |

## config

```sh
cs config
```

Prints the config's path, then every setting with its current value and a
one-line description, then each profile's Chrome profile:

```
  change one with:  cs config <name> <value>

  chrome, per profile (the Chrome profile Claude in Chrome is used from):
  change one with:  cs profile set <profile> chrome <name|folder|inherit>
```

`--json`: `{"path": …, "<setting>": "<value>", …, "chrome": {"<profile>": "<folder>" | null}}`.
Values are strings as `cs config` prints them (`85`, `3m0s`, `idle`).

## config get

```sh
cs config switch_at
cs config get switch_at --profile work
```

Prints one value. `get` is optional: `cs config <name>` is the same. With
`--profile`, it prints the value in force in that profile, its override if it
has one, else the global value.

`--json`: `{"key", "value", "scope": "global"}`, or with `--profile`
`{"key", "value", "scope": "profile", "profile", "override"}`, where
`override` is `null` when the profile inherits.

`chrome` has no global value: `cs config get chrome --profile work` prints the
profile's Chrome profile folder, and its `--json` adds `"name"`, Chrome's name
for it. It is set with `cs profile set <profile> chrome …`, never with
`config set`.

## config set

```sh
cs config switch_at 80
cs config set poll_idle 15m
cs config models "Modelname"
```

Changes one global setting. `set` is optional: `cs config <name> <value>` is
the same. The value is checked against the setting's type and range and
against the whole config before anything is written; a refused value names
the range (`invalid_value`). Only that one line of the file changes. It
prints:

```
  switch_at  85 → 80
  written to ~/.config/claudeswitch/config.toml
  a running daemon picks it up without a restart
```

`--json`: `{"key", "previous", "value", "path", "daemon_running"}`.

To set a value for one profile only, use
[`cs profile set`](profiles.md#profile-set).

## config schema

```sh
cs config schema --json
```

Every setting: its type, default, range, allowed values and whether a profile
may override it. The menu-bar app builds its Rotation, Polling and Advanced
panes from it. Without `--json` it prints one line per setting with its range
underneath.

`--json`: `{"settings": [{"key", "type", "default", "scopes", "description",
"min", "max", "min_exclusive", "enum"}, …]}`. `type` is `percent`, `number`,
`int`, `duration`, `enum` or `list`. `min` and `max` are in seconds for a
duration, `null` when unbounded.

## config clean

```sh
cs config clean --yes
```

Removes what older releases left in the config: per-account `scope` lines and
`[project]` tables, which no longer do anything. Accounts and profiles are
untouched; if the edit would lose either, it is discarded. With nothing to
remove it says so. It asks first; `--yes` skips the question, and with
`--json` or no terminal and no `--yes` it refuses with
`confirmation_required`.

`--json`: `{"removed": {"scope": [ids], "projects": N}, "path"}`.

## Settings

Every setting `cs config` knows. Defaults and ranges are from the `settings()`
table in `cmd/claudeswitch/config_cli.go` and `config.Defaults()` in
`internal/config/config.go`, and match the app's test fixture of
`cs config schema --json`
(`macos/Tests/ClaudeSwitchCoreTests/Fixtures/cli-config-schema.json`).
*Profile* means `cs profile set` can override it for one profile.

| setting | type | default | range | profile | what it does |
|---|---|---|---|---|---|
| `switch_at` | percent | `85` | (0, 100] | yes | rotate away at this much of the 5-hour window |
| `switch_at_weekly` | percent | `98` | (0, 100] | yes | ...and at this much of the weekly one |
| `hard_floor` | percent | `99` | (0, 100] | yes | above this, swap mid-turn rather than wait for an idle gap (at or above `switch_at`) |
| `hot_threshold` | percent | `60` | [0, 100] | | poll the account in use every `poll_hot` above this, rather than `poll_active` |
| `switch_when` | enum | `idle` | `idle`, `immediate` | | `idle` prefers swapping between turns; `immediate` does not wait |
| `max_switch_wait` | duration | `30s` | at least 0 | | stop waiting for an idle gap after this |
| `cooldown` | duration | `10m0s` | at least 0 | | minimum gap between rotations, to stop flapping |
| `poll_active` | duration | `3m0s` | at least 2m (120s) | | how often to read the account in use |
| `poll_hot` | duration | `1m0s` | above 0 | | ...and when it is near the trigger or burning fast |
| `poll_idle` | duration | `10m0s` | at least 2m (120s) | | how often to read the others |
| `api_budget` | int | `12` | at least 2 | | usage calls per 5 minutes, across every process |
| `landing_margin` | number | `10` | [0, 50] | yes | a switch target needs this many points below its own trigger (`0` = off) |
| `blind_failover_polls` | int | `3` | at least 0 | | fail over after this many unreadable polls of the account in use (`0` = hold) |
| `hot_reserve` | int | `10` | [0, 15] | | advanced: usage calls per account held back for hot polling |
| `unseen_calls_per_hour` | number | `2` | [0, 20] | | advanced: usage calls an hour set aside on a live account for Claude Code's own reads |
| `refresh_window` | duration | `1h0m0s` | above 0 | | renew a credential this long before it expires |
| `refresh_probe` | duration | `24h0m0s` | at least 0 (`0s` disables) | | also renew idle accounts this often, to catch a dead refresh token |
| `models` | list | empty | comma-separated | yes | models whose weekly limit counts like the weekly window; empty means none |

Durations are written like `90s`, `5m` or `2h`. Percentages may end in `%`.
Two values are per account or per profile only and are not `cs config`
settings: an account's `reserve` (edited in the file) and a profile's
`chrome` ([`cs profile set`](profiles.md#profile-set)). The order accounts are
tried in is [`cs priority`](accounts.md).

Why each default is what it is, and the advanced budget settings in depth:
[GUIDE → Configuration](../GUIDE.md#configuration) and
[GUIDE → Advanced](../GUIDE.md#advanced).

## Exit status

- `0`: done.
- `1`: refused or failed: an unknown setting (`not_found`, listing the
  settings), a refused value (`invalid_value`, naming the range), a config
  that will not load (`config_invalid`), a confirmation needed
  (`confirmation_required`). With `--json` the error is
  `{"error": {"code", "message", "hint", "retry_at"}}` on stdout; see
  [APP_CLI → Error codes](../APP_CLI.md#error-codes).

[← Documentation index](../README.md)
