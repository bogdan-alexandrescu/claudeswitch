# Settings → Advanced

The app's own preferences, and the advanced call-budget and credential-refresh settings.

See also: [Popover](popover.md) · [Menu bar](menu-bar.md) · [cs config](../cli/config.md) · [GUIDE → Advanced](../GUIDE.md#advanced)

<p align="center"><img src="../images/settings-advanced.png" width="880" alt="Settings, Advanced pane, This app: Appearance, Usage in the popover, Open Claude Code in, Icon only in the menu bar, the note that Open at login needs the app in Applications, the claudeswitch binary with Choose…, Check for updates with its line and Check now, Notifications with actions, the Switch to best hotkey and Quit"></p>

## This app

These are stored by the app, not in claudeswitch's config.

| row | what it does | default |
|---|---|---|
| **Appearance** | System follows your Mac. Light or Dark keeps ClaudeSwitch in one appearance whatever the system uses. Also in the menu-bar icon's menu (right-click) | System |
| **Usage in the popover** | Dials or Bars for the session and week. The switch in the popover and **Show usage as** in the icon's menu change it too | Dials |
| **Open Claude Code in** | the terminal **Open Claude Code** and **▶** use: Terminal, iTerm, Ghostty or Warp. It runs `cs run <profile>` there | Terminal |
| **Icon only in the menu bar** | hides the profile, account and percentage next to the rings | off |
| **Open ClaudeSwitch at login** | starts the app when you log in (under Daemon before 0.8.18). When the app does not run from its bundle (a `swift run` build) the row says it is available once the app is installed in Applications | off |
| **claudeswitch** | the binary the app runs (with `~` for your home folder) and its version. **Choose…** picks another one; **Reset**, shown once one is chosen, goes back to looking where installers put it. Without a choice the app looks in `~/.local/bin`, then `PATH` and the usual Homebrew and Go locations. A chosen file that cannot be run is passed over, and the row says so: `The chosen ~/bin/claudeswitch cannot be run; using ~/.local/bin/claudeswitch.` | found automatically |
| **Check for updates** | once a day, one call to GitHub's latest release. Off makes no call at all. The line under it gives this version, a newer one found, and when it last checked (`This is 0.8.18 · checked 1h ago`), or that the last check failed (`last check failed 5m ago`, why in its tooltip). **Check now** checks at once and opens the update window with what it found; it is greyed out while the check is off. See [Updates](updates.md) | on |
| **Notifications with actions** | the app's own notifications: Undo and Pin here when rotation switches, in place of the daemon's notice; Sign in when an account needs it. The daemon's other notices still appear. When macOS does not allow ClaudeSwitch's notifications, the row says `Not allowed in System Settings → Notifications; the daemon's own notices appear instead.` See [Shortcuts → Notifications](shortcuts.md#notifications) | on |
| **Switch to best hotkey** | a global key for Switch to best on the followed profile; click the key to record another. See [Shortcuts](shortcuts.md#the-hotkey) | off, ⌥⌘S |
| **Quit** | **Quit ClaudeSwitch** closes the menu-bar app, closing any open sheet or dialog first (an open one would refuse the quit). The daemon keeps switching accounts; open ClaudeSwitch from Applications to bring the icon back. Also in the icon's menu, and ⌘Q in the popover | |

The app needs claudeswitch 0.5.1 or later. The version shown in the
screenshot is example data.

## Settings

The `cs config` settings that are neither [Rotation](settings-rotation.md)
nor [Polling](settings-polling.md), built from `cs config schema --json` and
saved with `cs config set` when you press Return or leave a field, as in
[Rotation](settings-rotation.md). Change these only if you have measured
better.

| row | key | what it does | default | range |
|---|---|---|---|---|
| **Hot reserve** | `hot_reserve` | usage calls per account held back for hot polling | 10 | 0 to 15 |
| **Unseen calls per hour** | `unseen_calls_per_hour` | usage calls an hour set aside on a live account for Claude Code's own reads | 2 | 0 to 20 |
| **Refresh window** | `refresh_window` | renew a credential this long before it expires | 1h | above 0s |
| **Refresh probe** | `refresh_probe` | also renew idle accounts this often, to catch a dead refresh token (`0s` disables) | 24h | at least 0s |

The reasoning behind the budget settings:
[GUIDE → Advanced](../GUIDE.md#advanced). Credential renewal:
[GUIDE → Keeping credentials alive](../GUIDE.md#keeping-credentials-alive).

[← Documentation index](../README.md)
