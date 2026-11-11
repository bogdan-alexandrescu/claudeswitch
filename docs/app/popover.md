# Popover

The window that opens from the menu-bar glyph: the daemon, one card per profile, and the actions you use day to day.

See also: [Menu bar](menu-bar.md) · [Claude in Chrome in the app](chrome.md) · [cs status](../cli/status.md) · [cs accounts](../cli/accounts.md) · [Concepts](../concepts.md#drain-forecast)

<table>
  <tr>
    <td align="center"><img src="../images/popover-dials.png" width="384" alt="The popover with dials: daemon live, polled 1m ago; the default profile open on personal, session 24%, week 44%, Switch to Best greyed out with No account to switch to, and Next: stay · 54 points left on the week; the work profile as a compact row, work-team · session 7% · week 22%"></td>
    <td align="center"><img src="../images/popover-bars.png" width="384" alt="Another popover with bars: daemon in dry run; the default profile open on work-1, session 3%, week 41%, a per-model weekly limit at 12%, Switch to Best greyed out because personal is in use by review; the review profile as a compact row, pinned, its rings with a warning mark and needs sign-in · personal in red"></td>
  </tr>
  <tr><td align="center">Dials</td><td align="center">Bars</td></tr>
</table>

Dark mode: [dials](../images/popover-dials-dark.png),
[bars](../images/popover-bars-dark.png).

## Header

| control | what it does | default |
|---|---|---|
| **Live** / **Dry run** | the daemon's mode: green when live, orange in dry run, then how long since it polled (`polled 1m ago`). Red **Not polling for 21m ›** when it has stopped, or **No daemon ›** when none ever ran, in the text's full colour; click it to open [Settings → Daemon](settings-daemon.md), where it is started. Change the mode in [Settings → Daemon](settings-daemon.md) or with `cs daemon live` / `cs daemon dry-run` | as installed: dry run |
| dials / bars switch | the two small buttons at the top right: show session and week as dials or as bars. The same setting as **Show Usage As** in the [icon's menu](#the-icons-menu) and **Usage in the popover** in [Settings → Advanced](settings-advanced.md) | Dials |

A small spinner beside them shows while the app reads. Before its first read the popover says **Reading…**, and with no state file yet, **No state yet: is the daemon installed? Run `claudeswitch setup`.**

## Cards

One card per profile. The card of the profile the menu bar follows is open
and outlined in the accent colour. With more than one profile it is marked
**Menu bar**, and any other card you open has **Show in Menu Bar** next to
its name, which makes the menu bar follow it; with one profile neither
shows. The other profiles are compact rows. An opened card has a chevron to
close it.

A compact row has the live account's small rings (session outside, week
inside, as in the [menu-bar glyph](menu-bar.md#the-glyph); a warning mark
when the account needs sign-in or was refused, an empty ring when no
account is live), the profile's name (with a pin when it is pinned), one
line, a **▶** that opens Claude Code in that profile, in its last folder
(see [Open Claude Code](#open-claude-code-and-its-folder)), and a chevron that
opens the card. The ▶ is greyed out when the profile is not signed in, which
the row then says under it (`scratch is not signed in. Sign it in from
Settings → Profiles.`), unless its line already says the live account needs
sign-in. The line is
the live account with its session and week (`work-1 · session 3% · week
41%`). A pending switch or wait leads (`w01 → w02 · session …`, `waiting ·
w01 · session …`). Trouble takes the place of the figures, in red
(`needs sign-in · work-1`, `refused · work-1`). When the line is too narrow
for one row, the figures go under the account rather than cut it. A long
name is cut in its middle (`client-ex…integration`), so its start and end
both show; ids are too, everywhere in the popover.

An open card shows, top to bottom:

| element | what it shows or does | CLI |
|---|---|---|
| name and directory | the profile, and its config directory (`~/.claude` for `default`), which is left out rather than squeezed beside a long name | `cs profile list` |
| account picker | the live account and its plan. Pick another account of the profile's pools to switch to it now. Each line gives an account's figure, the window it is from and its state (`work-2  12% session · available`, `work-1  41% week · available`); a refused one, when it clears (`w03  100% · refused · session · clears in 36m`); and when the CLI can tell, how long it lasts ([the drain forecast](../concepts.md#drain-forecast)): `~21h left` of use before its switch point, `lasts to reset`, `∞` (not being spent) or `drained`, with `used elsewhere` when someone elsewhere is spending it; the live account's check sits in the menu's gutter, so every name starts in the same place. An account live in another profile that shares its pool says `in use by work` and cannot be picked | `cs use <id> --profile <p>`; forecasts from `cs account list --json` (`drain`) |
| globe | opens the live account's Chrome profile; greyed out while it opens. Hidden where Chrome is not supported | `cs chrome open <id>` |
| banners | Claude in Chrome, a lifted pin, and a refused action (below) | |
| **Session** and **Week** | the live account's two windows, as dials or bars (below). When the account needs sign-in their dots and figures are dimmed, since they are from its last reading; the labels and reset times stay legible | `cs status` |
| per-model limits | an account's per-model weekly limits, each with its own bar, in the same usage colours as the dials, and reset. One near or over the weekly trigger also has a warning triangle, so it does not rest on colour | `cs status` |
| sign-in or refusal line | an account that needs sign-in says **needs sign-in · last read 2h ago**, with **Sign In…**, which opens Settings → Accounts with [Add account](add-account.md) signing that account in again. A refused one says which window and when it clears (`refused · session · clears in 36m`). Any other failed reading gives its error (a timeout, say) | `cs login <id> --direct`; [cs setup → doctor](../cli/setup.md) |
| hours of use left | under the dials, how long the live account lasts at the speed the daemon measured: `~21h of use left at 2.6% an hour`, `lasts until the week clears at 0.5% an hour`, `∞, not being spent` or `drained: at its switch point`. Under 2 hours it is amber with a warning triangle. Hidden when the CLI does not know (no speed measured lately), and while the line above says the account needs sign-in, was refused or failed | `cs account list --json` (`drain`); `cs forecast` |
| pool line | how long the profile's pool lasts at those speeds: `work pool: ~83h of use left across 6 accounts`, `∞, not being spent (6 accounts with room)`, or `drained, no account under its switch point`. Under 2 hours of use, or drained, it is amber with a warning triangle. Hidden when the CLI does not know. Compact cards show it too | `cs why --json` (`pool_drain`); `cs forecast` |
| next refill | the soonest refill among the profile's other accounts, as much as it frees and when: `Next refill: cedar-max in 48m · 62% of session`. Hover it for the next four, named in full with their times; click it for [Refills](settings-accounts.md#refills) in Settings → Accounts, which says what counts. The live account's own resets are on its dials. With nothing to come there is no line | worked out from `state.json` |
| **Open Claude Code** | opens your terminal running Claude Code in this profile, in the folder named under it: `in claudeswitch ⌄`, the profile's last folder, or `in your home folder` when it has none. Click that line, or right-click the button, for [its folder menu](#open-claude-code-and-its-folder). The terminal is set in [Settings → Advanced](settings-advanced.md). Greyed out while it opens, and for a profile that is not signed in, which says under the buttons `work is not signed in. Sign it in from Settings → Profiles.` When the live account itself needs sign-in, its **Sign In…** above is the one remedy shown | `cd <folder> && cs run <p>`; folders from `cs profile recent` |
| **Switch to Best** | moves the profile to the account rotation would choose now, named under the button with its utilization (`work-2 · 12%`). It is the filled button only when the daemon would switch or wait anyway; while the daemon stays it is a quiet one. Greyed out, with the reason below it (beside Open Claude Code's, when both are greyed), when there is none: it never offers an account live in another profile (`work-2 is in use by work`), one that needs sign-in, or one outside the profile's pools. When the best has no more room than the live account it reads **Already on the Best**, greyed out, with the next best underneath (`next: work-2 · 12%`), which stays at full strength so it reads; room is points below each window's own trigger, as `cs why --json` reports it (`on_best`). When even the best has little room, the CLI's warning shows under it in amber | `cs use <best> --profile <p>` |
| **Next:** | what rotation will do next. A routine stay says how far the live account is from rotating, from the daemon's own figures: `Next: stay · 57 points left on the week` (or `on the session`; neither when the two triggers are the same). Any other decision gives the daemon's reason: `Next: switch → work-2 — …`, `Next: stay — pinned to …`, `Next: wait — …`. Hidden while `cs why` judged another account live than the card shows | `cs why --profile <p>` |
| **Pin** / **Pinned** | Pin holds the profile on its live account and turns rotation off for it, until that account is refused or needs sign-in: then the daemon lifts the pin and rotates. **Pinned** (filled in the accent colour) lets it rotate again. Next, Pin and ⋯ sit on the first line's baseline | `cs account pin <id>` / `cs account unpin --profile <p>` |
| **⋯** beside Pin | **Pin, Even If It Runs Out…** (on a pin already made, **Keep the Pin Even If It Runs Out…**): asks first ("Keep work on work-2 even if it runs out?"), then makes a pin the daemon never lifts, shown as **Pinned (Hard)**. Only with a CLI that reports `pin_hard` | `cs account pin <id> --hard` |

### Open Claude Code and its folder

<p align="center"><img src="../images/menu-folders-default.png" width="460" alt="The folder menu of default: Recent in default, then claudeswitch ~/code · 2h checked, website ~/code · 1d, api-gateway ~/clients/northwind · 3d, notes ~/code · 1w, a long name cut in its middle · 2w, then Choose Folder… and Home Folder"></p>

Claude Code starts in a folder, and **Open Claude Code** names the one it
will use: the profile's last folder. That is the newer of the folder you last
chose for the profile here and the folder Claude Code was last used in under
that profile (its newest session there). A chosen folder that no longer
exists is passed over; with none, it starts in your home folder.

The button's second line is a menu, and a right-click anywhere on the button
opens the same one:

- **Recent in default**: the folders Claude Code has been used in under this
  profile, newest first, each with where it is and how long ago
  (`website  ~/code · 1d`); a long name is cut in its middle. The one the
  button names is checked. A profile never opened in one says `No folders
  yet: work has not been opened in one`.
- **Choose Folder…**: any folder, starting at the current one.
- **Home Folder**.

Choosing one opens Claude Code there and remembers it for the profile, so
the button names it from then on. The collapsed card's ▶ and **Open Claude
Code** in [Settings → Profiles](settings-profiles.md) start in the same
folder, and Settings' button holds the same menu. The terminal runs `cd
'<folder>' && claudeswitch run <profile>` (Ghostty is given the folder as its
working directory).

The recent folders are one `cs profile recent --json` read when the popover
opens and when Settings → Profiles shows. A claudeswitch from before 0.8.20
has no such command: the menu then lists none, and the button says `in your
home folder` until you choose one. The image above is drawn by the app's
render mode as a facsimile of the menu, from the same lines, since a live
menu cannot be captured off screen.

### Dials and bars

<p align="center"><img src="../images/popover-chrome-signin.png" width="384" alt="The popover with both cards open; on the work card, the account picker on work-team, a Claude in Chrome banner, the session dial at 7% and the week dial at 22% with their threshold dots, the two buttons and Next: stay · 76 points left on the week"></p>

Each window is drawn as dots: 30 in a dial, 25 in a bar.

- **Lit dots** run up to the reading, in the rings' usage colours, as in the
  [menu-bar glyph](menu-bar.md#the-glyph): green when little is used, amber
  at half, red when the window is spent. A dial also has a larger dot at
  the reading. The figure is in the middle of a dial, with when the window
  resets under it, or at the right of a bar.
- **The threshold dot** is the larger dark dot (light green in dark mode). It
  sits at the profile's switch threshold: `switch_at` (85% by default) on the
  session, `switch_at_weekly` (98%) on the week. A profile with its own
  thresholds shows its own. When the lit dots reach it, rotation moves the
  profile on.
- **Trouble** does not rest on colour, whichever form you use: an account
  that needs sign-in has its dots and figures dimmed and a red line with **Sign
  In…**; a refused one says when it clears; a compact card's rings carry a
  warning mark.

## Banners

Banners appear on the card they concern. The two amber ones share one
look: a filled symbol, an orange border, and ×.

| banner | when | what to do |
|---|---|---|
| **Claude in Chrome in "Work" does not follow switches to work-team** (amber) | the profile's accounts share one Chrome profile, and rotation moved it to another account | **Give work-team Its Own Chrome Profile** creates one and opens it at the sign-in pages (`cs chrome add work-team`); after that claudeswitch opens it after every switch. The banner goes once that worked, and stays if it failed. × hides it until the next rotation. See [Claude in Chrome in the app](chrome.md) |
| **Pin on work-2 lifted: it was refused, so rotation runs again** (amber) | the daemon lifted the profile's pin because its account was refused or needed sign-in. Shown for a day, from the audit log's `unpin` event | × dismisses it; pin again, or **Pin, Even If It Runs Out** |
| **Could not switch work to work-2** (red) | an action on the card failed, for example `use` refused. It gives the CLI's message and hint, and when the refusal ends, **Try again at 14:05:00**, said once (not when the message already gives the time) and not after it has passed | wait, or follow the message. × dismisses it |

<table>
  <tr>
    <td align="center"><img src="../images/popover-warnings.png" width="384" alt="The popover with warnings: the daemon in dry run; the default profile on work-1, Switch to Best greyed out because personal is in use by review; the review profile open, its account personal needing sign-in, its dials dimmed, needs sign-in · last read 3m ago with Sign In…, pinned, review has no other account"></td>
  </tr>
</table>

When the `claudeswitch` binary is missing, too old, or speaks a newer
contract than this app, the popover shows that in place of the cards (their
buttons would run that binary), with how to install it, one command per
line, each with **Copy**; a command wider than the banner is cut in its
middle. **Add Account** is greyed out until it is fixed.

A green note says what an action did (`work now uses work-2`), and a red
one what failed where no card was involved. Both have ×; a note goes when
the popover closes, since it would be stale when it opens again.

## Footer

| control | what it does |
|---|---|
| **+ Add Account** | opens [Add account](add-account.md) for the followed profile. Greyed out until the binary is found |
| **Settings** (⌘,) | opens Settings at its [Overview](settings-overview.md), or brings an open Settings window forward on the pane it shows, as the icon's menu does |

Above them, an update found shows as **0.5.6 available · Update…**, an
install under way as its step and a progress bar, and a failed one as
**Could not update**; see [Updates](updates.md).

⌘R refreshes and ⌘Q quits while the popover is open.

A window opened from the popover comes up in front with the focus, in the
middle of the screen the pointer is on, and the popover closes.

### The icon's menu

Right-click the menu-bar icon, or Control-click it, for this menu (until
0.8.13 it was the ⋯ button at the popover's foot). A plain click opens the
popover.

<table>
  <tr>
    <td align="center"><img src="../images/menu-more.png" width="362" alt="The menu-bar icon's menu with Appearance open: System checked, Light, Dark"></td>
    <td align="center"><img src="../images/menu-more-usage.png" width="362" alt="The menu-bar icon's menu with Show Usage As open: Dials checked, Bars"></td>
  </tr>
</table>

These two images are drawn by the app's render mode as a facsimile of the
menu, built from the same list of lines as the menu itself, since a live
menu cannot be captured off screen.

| item | what it does | default |
|---|---|---|
| **Refresh Now** (⌘R) | reloads what the app shows: the state file, `cs why`, the Chrome and account lists and the profiles. It does not poll the usage API; the daemon does that | |
| **Appearance** | System, Light or Dark. System follows your Mac; Light or Dark keeps the popover, Settings and this menu in that appearance | System |
| **Show Usage As** | Dials or Bars | Dials |
| **Settings** (⌘,) | opens Settings at its Overview, or brings an open Settings window forward on the pane it shows | |
| **Set Up…** | opens the [first-run setup](first-run.md) window again | |
| **Check for Updates…** | checks for a newer release now and opens the update window with what it found (greyed out while the check is off in Advanced). A newer one also shows as **0.5.6 available · Update…** at the foot; see [Updates](updates.md) | |
| **About ClaudeSwitch** | the app's version | |
| **Quit ClaudeSwitch** (⌘Q) | quits the app, closing any open sheet or dialog first. The daemon keeps running. Also in Settings → Advanced | |

## What it reads

The popover reads `~/.local/state/claudeswitch/state.json` on every save, and
runs `cs why --json`, `cs chrome list --json` and `cs account list --json`
every 20 seconds and after each action. It never reads the keychain and never
calls the usage API. The commands it runs are listed in
[APP_CLI.md](../APP_CLI.md).

[← Documentation index](../README.md)
