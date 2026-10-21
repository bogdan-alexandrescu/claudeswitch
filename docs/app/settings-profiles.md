# Settings → Profiles

Create, edit and remove Claude Code profiles: their pools, their own thresholds and their Chrome profile.

See also: [cs profile](../cli/profiles.md) · [Concepts → profiles and pools](../concepts.md#profiles-and-pools) · [GUIDE → Multiple Claude Code profiles](../GUIDE.md#multiple-claude-code-profiles) · [PROFILES.md](../PROFILES.md)

<p align="center"><img src="../images/settings-profiles-pools.png" width="880" alt="Settings, Profiles pane: the default profile open with its pools, personal then team, then the account in no pool, and Change…; its thresholds, the Claude in Chrome picker and its buttons; the work profile closed below, using ops then team"></p>

**Settings...** in the popover opens this pane first. One card per profile.
A closed card shows the directory, the pools it uses (`pools personal, team,
then 1 in no pool`) and thresholds in one line, and **Edit** opens it. The rings at the top left of an open card are the live
account's week and session; the dot and text at the right say whether the
profile is signed in, and as which account.

## An open profile

| control | what it does | CLI | default |
|---|---|---|---|
| **POOLS** | the pools the profile uses, numbered in the order it uses them: an account in a later pool only when nothing in an earlier one is usable. A profile with its own pool shows it under its own name. `default` ends with the accounts in no pool (`then 1 account in no pool`). Hover a pool for its accounts. Which pool each account is in is set in [Accounts](settings-accounts.md) | `cs profile list` (`pools`) | |
| **Change...** | a sheet listing every pool with a checkbox; ↑ and ↓ order the checked ones. **Save** sets them. Checking another profile's own pool shares it: that profile keeps using it. Unchecking this profile's own pool, when it holds accounts, leaves them in a pool no profile uses (the sheet says so first). A refusal (an account live where it would stop being usable, `live`) shows in the sheet | `cs profile pools <p> <pool>...` (twice when its own pool is dropped: the CLI converts it first and keeps it first unless named) | |
| **FOLDERS** | the folders that pick this profile: `cs run` with no name in one of them runs this profile, and `cs profile which` says so. Globs, comma-separated (`~/work/**, ~/clients/**`); empty clears them. Hidden with a CLI that does not report `paths` | `cs profile set <p> paths <glob,glob>` | none |
| **Switch at session** | this profile's `switch_at`. Empty inherits the global value, shown as "90% (global)"; ↺ goes back to inheriting | `cs profile set <p> switch_at <n>` | inherit (85) |
| **Switch at week** | this profile's `switch_at_weekly` | `cs profile set <p> switch_at_weekly <n>` | inherit (98) |
| **Hard floor** | this profile's `hard_floor` | `cs profile set <p> hard_floor <n>` | inherit (99) |
| **Landing margin** | this profile's `landing_margin` | `cs profile set <p> landing_margin <n>` | inherit (10) |
| **Count model limits** | this profile's `models` | `cs profile set <p> models <list>` | inherit (none) |
| **Spend first** | this profile's `prefer`: **Inherit**, **Most room** or **Expiring quota** (a menu) | `cs profile set <p> prefer room\|expiring` | inherit (Most room) |
| **Chrome profile** | the Chrome profile Claude in Chrome is used from for this profile's accounts that have none of their own. Lists your Chrome profiles by name | `cs profile set <p> chrome <name>`; `inherit` for the default | Chrome's last used |
| **Open Claude Code** | your terminal running Claude Code in this profile | `cs run <p>` | |
| **Open Chrome** | opens the live account's Chrome profile | `cs chrome open <id>` | |
| **Shown in menu bar** | the menu bar follows this profile | | `default` |
| **Remove profile...** | removes the profile. Its own accounts (its own pool, and for `default` the accounts in no pool) move to the profile you choose; the pools it lists stay, for the profiles that share them. The profile's directory is kept | `cs profile remove <p> --to <other>` | |
| **Sign in with** (only when **Not signed in**) | signs the profile in with one of its accounts. One live in another profile says `in use by work` and cannot be chosen | `cs profile seed <p> <id>` | |

The threshold fields check what you type against the setting's range and show
the CLI's own message when it refuses. More on what each threshold does:
[Settings → Rotation](settings-rotation.md).

## New profile

<p align="center"><img src="../images/sheet-new-profile.png" width="520" alt="The New profile sheet: name, directory, the pools it uses, and which account to sign in with"></p>

| field | what it is | default |
|---|---|---|
| **Name** | the profile's name | |
| **Directory** | its Claude Code config directory; **Choose...** picks one | `~/.claude-<name>` |
| **Its accounts** (when some account is in no pool) | accounts in no pool, which `default` uses now, to make the new profile's own. With no `[[profile]]` blocks yet, that is every account. One live somewhere says so | none |
| **Pools** | the pools it uses, checked and ordered as in **Change...**. It shares them with the profiles that use them; nothing moves. Its own accounts come first | none |
| **Sign in with** | the account the new profile starts signed in as: one of its own, or of the pools checked, but not one live in another profile | None (sign in later) |

**Create** runs `cs profile create <name> [--dir <dir>] [--pool <ids>]`,
then `cs profile pools <name> <pools...>` and `cs profile seed <name> <id>`
when chosen; a step that fails says what was done. What creating does to
the directory (links to your settings, a copy of your MCP servers):
[GUIDE → Making a profile](../GUIDE.md#making-a-profile-and-starting-claude-code-in-one).

## Old profiles still holding a credential

A removed or repointed profile's last login stays guarded, so that account
is not swapped into another profile while it might still be live there. This
section lists them; **Forget...** releases one (`cs profile forget <p>`).

[← Documentation index](../README.md)
