# Settings → Overview

The Settings window's first page: what needs you, how each area stands, and
what happened last. Each tile opens its own pane.

See also: [Popover](popover.md) · [Settings → Profiles](settings-profiles.md) · [Updates](updates.md)

<p align="center"><img src="../images/settings-overview.png" width="880" alt="Settings, Overview: tiles for Daemon (Live, polled 1m ago), Profiles (2 profiles, default: work-1 · work: work-team), Accounts (4 accounts, all available · 2 pools), Rotation (Next: stay, switches at 85% · week 98%), Cleanup (Off, last run 2h ago · 512 MB) and Updates (Up to date, This is 0.8.13 · checked 1h ago), then Recent with a cleanup and three switches"></p>

**Needs you** appears only when something does, each with a button that
goes where it is fixed:

| it says | the button |
|---|---|
| the daemon is not installed, or has not polled for a while | **Open Daemon** |
| an account needs signing in again | **Sign in…** (Accounts) |
| Claude in Chrome does not follow switches to an account | **Open Accounts** |
| a newer ClaudeSwitch is available | **Update…** (the [update window](updates.md)) |

The tiles, each opening its pane (Updates opens the update window):

| tile | what it shows |
|---|---|
| **Daemon** | Live, Dry run, Not installed or Not polling, and when it last polled |
| **Profiles** | the profile, or how many, and which account each is on, with its session and week when there is one |
| **Accounts** | how many, and how many need signing in, have no headroom or were refused; the number of pools |
| **Rotation** | the next decision for the first profile, and when its pool runs dry at this pace, or the thresholds |
| **Cleanup** | Daily, Every …, or Off, and the last run's age and size ([Settings → Cleanup](settings-cleanup.md)) |
| **Updates** | Up to date, a newer version, Not checked yet (before the first check), or Check is off; this version and the last check |

**Recent** lists the last few switches (from `cs history --usage`) and the
last cleanup run, newest first.

[← Documentation index](../README.md)
