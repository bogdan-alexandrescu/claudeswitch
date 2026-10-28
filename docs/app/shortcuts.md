# Shortcuts, Raycast and the hotkey

Switch to best, pin, unpin and status without opening the popover: through
a `claudeswitch://` URL, which Shortcuts, Raycast and `open` can all use,
or a global hotkey.

See also: [Popover](popover.md) · [Settings → Advanced](settings-advanced.md) · [Notifications](#notifications)

## The URL scheme

| URL | what it does | same as |
|---|---|---|
| `claudeswitch://switch-best?profile=work` | switches the profile to its best account, unless it is already on it | **Switch to best** |
| `claudeswitch://pin?profile=work` | pins the profile to its live account | **Pin** (`account pin <live> --json`) |
| `claudeswitch://unpin?profile=work` | lets rotation move it again | **Pin** off (`account unpin --profile work --json`) |
| `claudeswitch://status?profile=work` | shows a notification: `work · work-1 · session 18%, week 41%` | |

Leave out `?profile=` for the profile the menu bar follows. A profile that is
not a name is refused. When the URL starts the app, the command runs once
the app has read the state.

Anything the app cannot do comes back as a notification, saying why:

- `work is already on its best account.`
- `work has no account to switch to: work-2 is in use by default` (the
  reason **Switch to best** gives under the button), or `…to switch to.`
  when there is none to give;
- `There is no profile called lab.`
- `work has no live account to pin.`
- `Could not switch work to work-2: …`, with the CLI's message, when it
  refuses the switch;
- `ClaudeSwitch could not read the accounts yet, so nothing was switched.`,
  when a command that arrived before the app's first reading (the URL
  launched the app) has waited 30 seconds for one that never came.

### Shortcuts

Add the **Open URLs** action with `claudeswitch://switch-best?profile=work`.
The shortcut can then run from the menu bar, Spotlight, a keyboard
shortcut or Siri.

### Raycast

Use a Quicklink with the URL, or a script command:

```sh
#!/bin/sh
# @raycast.schemaVersion 1
# @raycast.title Claude: switch to best
# @raycast.mode silent
open "claudeswitch://switch-best"
```

For the figures as text in Raycast, run `cs status` in a script command
with `@raycast.mode fullOutput` instead.

### Terminal

```sh
open "claudeswitch://status?profile=work"
```

## The hotkey

Settings → Advanced → This app → **Switch to best hotkey**: anywhere on your
Mac, switches the followed profile to its best account. Off by default.
Click the key to record another (⌘, ⌥ or ⌃ and a key; Escape cancels). A
key pressed without ⌘, ⌥ or ⌃ is not taken, and the row says `Hold ⌘, ⌥ or
⌃ with the key.` A key another app or macOS already holds is refused with a
note: `⌥⌘S is taken by another app or by macOS. Choose another.` When there
is no account to switch to, it says why, as the URL does.

| setting | default |
|---|---|
| **Switch to best hotkey** | off |
| the key | ⌥⌘S |

It uses Carbon's `RegisterEventHotKey`, so it needs no Accessibility
permission.

## Why not App Intents

App Intents (Shortcuts actions that return a value, Spotlight actions)
need Xcode's metadata processor (`appintentsmetadataprocessor`), which
writes the intents' description into the bundle at build time. The Command
Line Tools ship the AppIntents framework but not that tool, and the app
builds with the Command Line Tools alone, so intents compiled into it would
never appear in Shortcuts. The URL scheme is the fallback the spec names.

## Notifications

The daemon announces a rotation through `osascript`, which cannot carry
buttons. The app posts its own notification when it sees the change in
`state.json`:

| notification | buttons | runs |
|---|---|---|
| **work → work-2** (a rotation) | **Undo**, **Pin here** | `use <previous> --profile work --json`; `account pin work-2 --json` |
| **work-1 needs sign-in**, **work-1 was refused** (the live account) | **Sign in** | opens Add account for that account |
| **Sign personal in again** (its login expires within 5 days) | **Sign in…** | opens Add account for that account |

A switch you made in the app (the popover, a shortcut, Undo) is not
announced back, unless the CLI refused it: a refused switch is forgotten, so
it does not hide the next real rotation. An idle account that is refused is
not announced; one that can no longer sign in is. Each appears once per
change.

**Undo** and **Pin here** act from the notification, so when the CLI
refuses one, the app says so in another notification (`Could not switch
work to work-1: …`, `Could not pin work-2: …`) rather than only on a
popover card you may not open for a while.

A rotation is announced once. While the app posts these notifications
(Notifications with actions on, and allowed in System Settings), it runs
`cs app heartbeat --json` every minute, which records in `state.json` that
the app is notifying for the next two minutes, and the daemon skips its own
rotation notice meanwhile. Quit the app, or turn its notifications off with
Settings → Advanced → **Notifications with actions**, and the daemon's
notice comes back within two minutes. Only the rotation notice moves to the
app: every other daemon notice ("<profile>: every account is refused", or "<profile>: no
account free" when the accounts left are in use in another profile; a pool
running dry; a lifted pin; Claude in Chrome) still comes from the daemon.

[← Documentation index](../README.md)
