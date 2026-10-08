# cs daemon

Install, run, switch between dry run and live, and remove the background daemon that polls and rotates.

See also: [Concepts → dry run and live](../concepts.md) · [Settings → Daemon](../app/settings-daemon.md) · [GUIDE → Running as a daemon](../GUIDE.md#running-as-a-daemon) · [GUIDE → Notifications](../GUIDE.md#notifications) · [APP_CLI → daemon](../APP_CLI.md#daemon)

The daemon is `claudeswitch watch` run by the service manager: a launchd
agent on macOS (`xyz.claudeswitch.daemon`,
`~/Library/LaunchAgents/xyz.claudeswitch.daemon.plist`) or a systemd `--user`
unit on Linux (`claudeswitch.service`, in `~/.config/systemd/user/`). It polls
every account within the call budget, renews credentials, decides per profile
and, when live, swaps. Its log is `~/.local/state/claudeswitch/daemon.log` (on
Linux, `journalctl --user -u claudeswitch`).

```
claudeswitch daemon status|start|stop|restart|live|dry-run|install|uninstall [--live|--dry-run] [--json]
claudeswitch watch [--live] [--idle-gap D] [--quiet] [-v] [--config PATH]
claudeswitch uninstall [--credentials] [--yes] [--config PATH]
```

## daemon

| flag | meaning | default |
|---|---|---|
| `--json` | one JSON object on stdout | off |
| `--live` | `install`: install in live mode (it acts) | see `install` |
| `--dry-run` | `install`: install in dry-run mode (it reports, changes nothing) | see `install` |
| `--yes` | accepted for symmetry; nothing here asks | off |

`--live` and `--dry-run` together are refused.

| verb | what it does |
|---|---|
| `status` | whether the service is installed and loaded, whether a daemon is running, its mode and the binary it runs |
| `install` | registers **this** binary as the service and loads it. Without a flag it keeps the mode of an existing install, and a new install is dry run |
| `uninstall` | unloads the service and removes its file. State, config and credentials stay |
| `start` | loads (macOS) or starts (Linux) the installed service |
| `stop` | unloads or stops it |
| `restart` | reloads it, so it runs the file as it now is. Use it after replacing the binary |
| `live` | rewrites the service in live mode and reloads it |
| `dry-run` | rewrites it in dry-run mode and reloads it |

`start`, `stop`, `restart`, `live` and `dry-run` need the service installed
(`not_installed`, with the `daemon install` command). `install` refuses a
binary in a temporary or translocated location, which would be gone after a
restart (`binary_not_durable`): install the binary in `~/.local/bin` first.
On macOS, run `cs whoami` once before `install` and click **Always Allow**,
because the daemon cannot answer the Keychain prompt
([README → Daemon](../../README.md#daemon)).

Every verb ends by printing the status (paths shortened here; it prints them
in full):

```
  daemon (launchd)
    file     ~/Library/LaunchAgents/xyz.claudeswitch.daemon.plist
    binary   ~/.local/bin/claudeswitch
    mode     dry-run
    loaded   true
    running  true
```

or `not installed; install it with: claudeswitch daemon install`.

`--json`: `{"action", "platform", "file", "log", "installed", "loaded",
"running", "mode", "binary", "this_binary", "binary_is_this"}`, and while a
daemon runs, `"daemon_live"`, `"daemon_version"` and `"since"`. `mode` is
`"live"`, `"dry-run"`, or `null` when not installed. Full contract:
[APP_CLI → daemon](../APP_CLI.md#daemon).

### Dry run first

```sh
cs daemon install          # dry run
cs audit --kind decision   # a day later: what it would have done
cs daemon live             # let it act
cs daemon dry-run          # back to reporting only
```

In dry run it makes every decision and logs the swap it would make, but
changes nothing. It still renews credentials. `cs status`, the popover and
the session-start context say which mode it is in. From a source checkout,
`./install.sh` and `./install.sh --live` do the same as `daemon install`;
re-running `./install.sh` without `--live` puts a live daemon back into dry
run.

## watch

```sh
claudeswitch watch            # in a terminal, dry run
claudeswitch watch --live
```

The daemon itself, in the foreground. The service runs exactly this, with
`--live` in live mode. Run it by hand to watch its log as it works, with the
service stopped: only one daemon may run per machine, enforced with a lock
file, and a second `watch` exits with an error.

| flag | meaning | default |
|---|---|---|
| `--live` | actually perform swaps | off (report only) |
| `--idle-gap D` | quiet period that counts as between turns | `8s` |
| `--quiet` | no desktop notifications | off |
| `-v` | verbose logging | off |
| `--config PATH` | the config file | `~/.config/claudeswitch/config.toml` |

It re-reads the config when the file changes, so accounts and profiles that
`login`, `add` or `profile` write are picked up without a restart. It stops on
SIGINT or SIGTERM. What it notifies about:
[GUIDE → Notifications](../GUIDE.md#notifications).

## uninstall

```sh
cs uninstall
cs uninstall --credentials
```

Removes what claudeswitch put on the machine: the daemon service (unloaded
first) and `~/.local/state/claudeswitch` with the logs and state. It keeps
your config and your vaulted credentials, and always leaves Claude Code's own
credential alone. It lists what it will remove and keep, then asks.

| flag | meaning | default |
|---|---|---|
| `--credentials` | also delete every vaulted credential (you would have to sign in again) | off |
| `--yes` | do not ask | off |
| `--config PATH` | the config whose accounts `--credentials` removes | default path |

Without `--yes` and without a terminal it refuses with
`pass --yes to uninstall without being asked`. Answering no prints
`nothing removed` and exits 0. It does not remove the binary, the `cs` link,
the app, the plugin or the status line; the full list is in
[README → Uninstall](../../README.md#uninstall).

## Exit status

- `0`: done (including `uninstall` answered no).
- `1`: refused or failed: `not_installed`, `unsupported_platform` (no launchd
  or systemd), `service_failed` (launchctl or systemctl failed; the hint is
  its output), `binary_not_durable`, or `watch` finding a daemon already
  running. With `--json` the error is
  `{"error": {"code", "message", "hint", "retry_at"}}` on stdout; see
  [APP_CLI → Error codes](../APP_CLI.md#error-codes).
- `2`: `watch` or `uninstall` with a flag it does not know.

[← Documentation index](../README.md)
