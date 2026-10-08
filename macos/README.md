# ClaudeSwitch for the menu bar

A native macOS menu-bar app (SwiftUI `MenuBarExtra`, macOS 13 or later) that
shows what claudeswitch knows and switches accounts with one click.

- **Menu bar:** the active account and its binding utilization, e.g.
  `work-1 41%`. The gauge icon moves as the binding window nears the switch
  threshold (within 10 points), and turns into a warning triangle with a
  trailing `!` once it is over it or the account needs a login. An icon-only
  mode is a checkbox in the menu.
- **Menu:** the daemon (live or dry-run, "not polling for 9m" when it has
  stopped, how long since it last polled), what rotation will do next and why, every Claude Code profile when
  there is more than one, and every account with its 5-hour and weekly bars
  (the tick is the switch threshold), reset countdowns, state (available,
  refused, needs login, no headroom, window reset) and the policy's verdict.
  Per-model weekly limits are listed under the account when the API reports
  them.
- **Switch:** each other account has a Switch button, which runs
  `claudeswitch use <id>` and shows what it printed, success or error.
- **Launch at login:** a checkbox (SMAppService). Off until you tick it.
- **Live:** it watches `~/.local/state/claudeswitch/` and re-reads
  `state.json` on every save. Every 20 seconds, and right after a switch, it
  also runs `claudeswitch why --json`. Never on a file event: `why` can save
  `state.json` itself (when it discards a stale record), and running it on
  every save would race the daemon's saves and trigger itself.

## What it touches

It never reads the keychain and never calls the usage API. It reads
`~/.local/state/claudeswitch/state.json`, runs `claudeswitch why --json` and
`claudeswitch config --json` (both read only `config.toml` and `state.json`),
and changes things only by running `claudeswitch use`. It deliberately does not
run `claudeswitch status --json`, which reads each account's keychain item and,
by default, refreshes stale readings through the usage API.

Whether the daemon is working is judged from `state.json` alone. The daemon
saves it every two minutes whatever else it is doing, so when the newest of
`saved_at` and the newest reading is older than three of those (or three
`poll_active` intervals, if that is longer: 6 minutes by default) the menu says
"not polling for N m". No `daemon_since` means no daemon has ever run. A CLI
command can save the file too, so a fresh save does not prove the daemon is
alive; a stale one does prove it is not. The app never touches `daemon.lock`:
the daemon takes that lock once at startup without retrying, and a probe that
overlapped startup would make it exit.

It uses the binary you choose with **Binary…** in the menu when that is set
and present, then `~/.local/bin/claudeswitch`, then `PATH` (plus
`/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin`, `~/bin`, since an app started
from Finder does not get your shell's `PATH`). Its version is checked once per
binary, again whenever the file changes. Without one, or with one older than
0.4.0, the menu says so and shows how to install it.

## Install: build it on your Mac (recommended)

```sh
./install-app.sh
```

from a checkout of this repository. It needs only the Command Line Tools
(`xcode-select --install`), not Xcode. It compiles the app with SwiftPM, wraps
it in `ClaudeSwitch.app` (no Dock icon), signs it ad hoc, installs it in
`/Applications` (or `~/Applications` if that is not writable) and offers to
open it. Because it was built on your Mac rather than downloaded, macOS does
not quarantine it and it opens without a Gatekeeper warning. No Apple
Developer account is involved.

`./install-app.sh --open` / `--no-open` skip the question;
`./install-app.sh --uninstall` quits and removes it. Re-run it after pulling to
update.

## Install: from a release zip

Releases may carry `ClaudeSwitch-<version>-macos.zip`, a universal build. It is
not signed with a Developer ID or notarized, so the first launch of a
downloaded copy is blocked. Once:

1. Unzip it and move `ClaudeSwitch.app` to `/Applications`.
2. Open it. macOS says it cannot verify the developer; click **Done**.
3. Open **System Settings → Privacy & Security**, scroll to the message about
   ClaudeSwitch, click **Open Anyway**, and confirm.

Or, instead of steps 2–3, remove the quarantine flag yourself:

```sh
xattr -dr com.apple.quarantine /Applications/ClaudeSwitch.app
```

Building from source avoids all of this.

To produce the zip: `macos/scripts/app-zip.sh` (writes `macos/dist/`, with a
`.sha256` beside it).

## Development

```sh
cd macos
swift build                     # debug build
scripts/test.sh                 # unit tests (swift test, with or without Xcode)
scripts/build-app.sh            # macos/build/ClaudeSwitch.app
.build/debug/ClaudeSwitchBar --render Tests/ClaudeSwitchCoreTests/Fixtures /tmp/out
                                # draw the menu and label from fixtures to PNGs
```

`ClaudeSwitchCore` holds everything testable: decoding, the view model,
formatting, finding and running the binary. `ClaudeSwitchBar` is the SwiftUI
app. Decoding is deliberately lenient: a missing or retyped field reads as
unknown rather than failing the read, so a newer or older binary degrades the
display instead of breaking it.

The test fixtures are real output of the Go code, made by
`scripts/gen-fixtures.sh`: it builds claudeswitch from this checkout and runs
it against a throwaway `HOME` with placeholder accounts (never your own
`state.json`). `why-profiles.json` is the per-profile shape (two `[[profile]]` blocks),
generated with `SUFFIX=-profiles scripts/gen-fixtures.sh`; the snapshot tests
pin `fixtureNow` to the time the fixtures were generated.
Tests never run the real binary and never touch `~/.config/claudeswitch` or
`~/.local/state/claudeswitch`.
