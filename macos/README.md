# ClaudeSwitch for the menu bar

A native macOS menu-bar app (SwiftUI `MenuBarExtra`, macOS 13 or later) that
shows what claudeswitch knows and manages it all: profiles, accounts,
settings and the daemon (IMPROVEMENTS M1, M3-M7).

- **Menu bar:** the live account of the profile you choose to follow, and its
  binding utilization, e.g. `work · work-1 41%` (no profile name with one
  profile). The gauge icon moves as the binding window nears the switch
  threshold (within 10 points), and turns into a warning triangle with a
  trailing `!` once it is over it or the account needs a login. Icon only is
  a setting (Advanced).
- **Popover:** the daemon (live or dry run, how long since it polled, "not
  polling for 9m" when it has stopped) and a card per profile. The followed
  profile's card is open: its directory, the live account as a picker of the
  profile's pool (`use <id> --profile P --json`), session and week bars with a
  tick at that profile's own threshold, resets, per-model weekly limits,
  **Open Claude Code** (your terminal running `claudeswitch run <profile>`:
  Terminal, iTerm, Ghostty or Warp), **Switch account**, Open Chrome for the
  account (`chrome open`, or `chrome add` the first time), Pin (`account pin` /
  `account unpin --profile`), and what rotation will do next and why. The
  other profiles are compact rows that expand. **+ Add account** and
  **Settings…** at the foot.
- **Settings window:** Profiles (create with a directory, pool and seed
  account; add, remove and move pool accounts; the five per-profile
  overrides; forget old profiles' credentials), Accounts (drag to set the
  rotation order; rename, sign in again, Chrome, delete after a
  confirmation naming account and seat; recovery copies), Rotation, Polling
  and Advanced (every `cs config` setting, generated from
  `config schema --json`, with inline validation and the CLI's own errors),
  Daemon (status, live or dry run, restart, start/stop, install/uninstall,
  launch the app at login).
- **Add account:** sign in with the browser and paste the code back
  (`login --direct --no-open`, the app opens the page in the browser you
  pick; your live session is not touched), or save the login Claude Code is
  signed into now (`add`, with the name it suggests).
- **Live:** it watches `~/.local/state/claudeswitch/` and re-reads
  `state.json` on every save. Every 20 seconds, and right after an action, it
  also runs `why --json` and `chrome list --json`. Never on a file event:
  `why` can save `state.json` itself (when it discards a stale record), and
  running it on every save would race the daemon's saves and trigger itself.
  `profile list --json` looks up each profile's sign-in, so it runs when the
  popover or Settings opens, after a change, and otherwise every 5 minutes.

## The menu-bar item

- **Its label** is a gauge and the followed profile's live account with
  its utilization. Before the first data arrives it shows the gauge with
  `cs …` (loading). The question mark appears only when the `claudeswitch`
  binary is missing or too old; another failed read keeps the last figures,
  or shows `cs`.
- **Placement and the notch.** macOS puts a new menu-bar item to the left
  of the others, and on a MacBook with a notch and a full menu bar that can
  be under the notch, where it is drawn but cannot be seen. So on its first
  launch, when no position is stored, the app stores one near the right edge
  (300 pt from it) in its defaults, under the key AppKit itself uses:

  ```sh
  defaults read xyz.claudeswitch.menubar "NSStatusItem Preferred Position Item-0"
  ```

  A position you chose by ⌘-dragging the item is stored under that same key
  and is never overwritten. A few seconds after launch the app checks the
  item's frame against the notch (the gap between the screen's auxiliary
  top-left and top-right areas). If it is under the notch it logs an error
  (subsystem `xyz.claudeswitch.menubar`, category `placement`) and, once,
  explains how to make room — quit or hide other menu-bar apps, or ⌘-drag
  the item right of the notch — and offers **Move It**, which stores the
  right-edge position and reopens the app. By hand:

  ```sh
  defaults write xyz.claudeswitch.menubar "NSStatusItem Preferred Position Item-0" -float 300
  ```

  then quit and reopen ClaudeSwitch.

## What it touches

It never reads the keychain, never calls the usage API, never opens
Chrome's files and never touches the daemon's lock. It reads
`~/.local/state/claudeswitch/state.json` and does everything else by running
`claudeswitch … --json` commands from docs/APP_CLI.md, which answer with one
JSON object or an error object (`code`, `message`, `hint`) that the app shows
as it is; it never parses human text. It deliberately does not run
`claudeswitch status --json`, which reads each account's keychain item and,
by default, refreshes stale readings through the usage API. (Some of the
commands it runs read the keychain themselves, in the binary: `profile list`
for sign-in, `login`, `add`, `account delete`, `recovery`.)

Whether the daemon is working is judged from `state.json` alone. The daemon
saves it every two minutes whatever else it is doing, so when the newest of
`saved_at` and the newest reading is older than three of those (or three
`poll_active` intervals, if that is longer: 9 minutes at the 3m default) the menu says
"not polling for N m". No `daemon_since` means no daemon has ever run. A CLI
command can save the file too, so a fresh save does not prove the daemon is
alive; a stale one does prove it is not. The app never touches `daemon.lock`:
the daemon takes that lock once at startup without retrying, and a probe that
overlapped startup would make it exit.

It uses the binary you choose in Settings → Advanced when that is set
and present, then `~/.local/bin/claudeswitch`, then `PATH` (plus
`/opt/homebrew/bin`, `/usr/local/bin`, `~/go/bin`, `~/bin`, since an app started
from Finder does not get your shell's `PATH`). Its version is checked once per
binary, again whenever the file changes. Without one, or with one older than
0.5.1 (the first with the JSON CLI), the menu says so and shows how to
install or update it.

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
                                # draw the popover, every Settings pane, the
                                # add-account sheet and the label, light and
                                # dark, from fixtures to PNGs
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
pin `fixtureNow` to the time the fixtures were generated. The `cli-*.json`
fixtures are the JSON CLI's answers: the config-only commands (`config
schema`, `config get`/`set`, `priority`, `account scope`, `chrome list` and
their refusals) are generated the same way; the ones that would reach the
keychain, launchd or Chrome (`profile list`, `login`, `add`,
`account delete`, `recovery`, `daemon`, `chrome add`/`open`) are written by
hand from docs/APP_CLI.md. The CLI tests run each call against a fake
`claudeswitch` script that records its argument vector and answers with a
fixture.
Tests never run the real binary and never touch `~/.config/claudeswitch` or
`~/.local/state/claudeswitch`.
