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

Anything the app cannot do (already on the best, no such profile) comes back
as a notification. A refused switch shows in the popover, as it does when
you click.

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
key another app or macOS already holds is refused with a note.

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
| **work-1 needs a sign-in**, **work-1 was refused** (the live account) | **Sign in** | opens Add account for that account |
| **Sign personal in again** (its login expires within 5 days) | **Sign in…** | opens Add account for that account |

A switch you made in the app (the popover, a shortcut, Undo) is not
announced back. An idle account that is refused is not announced; one that
can no longer sign in is. Each appears once per change.

A rotation is announced once. While the app posts these notifications
(Notifications with actions on, and allowed in System Settings), it runs
`cs app heartbeat --json` every minute, which records in `state.json` that
the app is notifying for the next two minutes, and the daemon skips its own
rotation notice meanwhile. Quit the app, or turn its notifications off with
Settings → Advanced → **Notifications with actions**, and the daemon's
notice comes back within two minutes. Only the rotation notice moves to the
app: every other daemon notice (all accounts burnt, a pool running dry, a
lifted pin, Claude in Chrome) still comes from the daemon.

[← Documentation index](../README.md)
