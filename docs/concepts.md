# Concepts

The ideas the app and the CLI are built on, each in a few paragraphs, with links to the depth.

See also: [Tutorial](TUTORIAL.md) · [GUIDE](GUIDE.md) · [DESIGN.md](DESIGN.md) · [PROFILES.md](PROFILES.md)

## Accounts and seats

An **account** is one Claude login that claudeswitch has stored under a name
you chose, such as `personal` or `work-team`. Its identity is its **seat**:
one person in one organization. One email can belong to several organizations,
each with its own quota, so claudeswitch never identifies an account by email.
`setup`, `login` and `add` write each account into the config pinned to its
seat, and refuse a credential whose seat does not match.
[GUIDE → Adding an account](GUIDE.md#adding-an-account).

## Profiles and pools

A **profile** is one Claude Code config directory: `default` is `~/.claude`
(the Claude Code you get when `CLAUDE_CONFIG_DIR` is unset), and a profile
such as `work` has its own, such as `~/.claude-work`. Each profile has a
**pool**: the accounts it may rotate between. Pools never overlap. One daemon
drives every profile.

With no `[[profile]]` blocks in the config, everything is one implicit
`default` profile. `cs profile create` declares the first real one.
`cs run <profile>` starts Claude Code in a profile.

An account is live in at most one profile at a time. Refreshing a token
revokes the previous one, so an account live in two profiles would be logged
out of one of them; claudeswitch refuses any swap that could cause that.
[GUIDE → Multiple Claude Code profiles](GUIDE.md#multiple-claude-code-profiles),
[PROFILES.md](PROFILES.md).

## The two windows and thresholds

Every account has two usage windows, reported by the usage API:

- the **session**, a rolling 5-hour window;
- the **week**, a 7-day window.

Some accounts also have **per-model weekly limits**. They are shown, and only
count like the week when the model is listed in `models`.

A profile rotates away from its live account when the session reaches
**`switch_at`** (85% by default) or the week reaches **`switch_at_weekly`**
(98%). The window that is closer to its threshold is the **binding** one:
it is the figure in the menu bar and the one `▸` marks in `cs status`. In
the app, the larger dot on each dial or bar is that threshold. Above
**`hard_floor`** (99%) it swaps mid-turn rather than wait for a pause.

Every threshold can be set per profile, in Settings → Profiles or with
`cs profile set <p> <key> <value>`.

## How rotation decides

Each time the daemon reads the live account, it decides for each profile:

- **stay**: the live account is under its thresholds;
- **switch** to an account: the live account is over a threshold and another
  account in the pool is eligible;
- **wait**: it should move, but no account is eligible; it says which one
  recovers first.

An account is **eligible** when it can be read, is not refused, is under its
own `reserve` if it has one, and has at least **`landing_margin`** points (10)
of room below the session threshold, so the profile does not leave it again
at once. Of the eligible accounts, the switch takes the one with the most
room; **`priority`** order (Settings → Accounts, drag to reorder) breaks
ties. With **`prefer = "expiring"`** (Settings → Rotation; per profile with
`cs profile set`) it takes instead the one whose weekly window resets
soonest while it still has quota unused, so quota that would expire is
spent first, and room breaks ties; `why` says so (`work-team resets in 9h
with 40% unused, so it goes first`). The default, `prefer = "room"`, is the
most-room rule.

A **pinned** profile does not rotate, with one exception, the pin safety
valve: if the pinned account is refused, out of quota (100%) or needs a
sign-in, the daemon lifts the pin, rotates as usual and says why
(`pin on work-1 lifted: it was refused`) in its log, a notification and the
audit log (`kind: unpin`). `cs account pin --hard` keeps the pin even then.
After a rotation, **`cooldown`** (10 minutes) must pass before the next.

With **`switch_when = idle`** a due switch waits for a gap between Claude
Code's turns, for up to **`max_switch_wait`** (30 seconds), then goes ahead.

`cs why` prints the decision and the reasoning for each account. The app's
**Next:** line and `cs plan` show the same. `cs audit --kind decision` lists
past decisions.

## Runway

`cs status` forecasts each profile's **runway**: when its pool has no
eligible account left at the pace the live account is burning now
(`work pool: runs dry Thu 14:00 at this pace`), spending each account to its
trigger in the order rotation would. A forecast needs the live account's
last two readings, both recent; with fewer, or with an account in the pool
not yet read, there is none: unknown stays unknown. When a window resets
before the pool would run dry, the pool gets room back, and `status` says
that instead. The daemon notifies once when the forecast falls within two
hours. `cs status --detail` adds when each account reaches its trigger.

## Hot swaps

A swap writes the new account's credential into the profile's live slot
(the macOS Keychain item or `.credentials.json` that Claude Code reads). Only
the `claudeAiOauth` part changes, so MCP logins stay. Running Claude Code
sessions pick it up on their next request: no restart, and the conversation
carries on.

## Dry run and live

The daemon installs in **dry run**. It polls, decides and logs the swap it
would make, and changes nothing. Run it that way until `cs audit --kind
decision` matches your judgement, then `cs daemon live` (or the **Live**
switch in Settings → Daemon). `cs daemon dry-run` goes back. Credential
renewal runs in both modes. Hand-made switches (`cs use`, the account picker,
**Switch to best**) always act.
[GUIDE → Running as a daemon](GUIDE.md#running-as-a-daemon).

## Credentials and recovery

Stored credentials live in the macOS Keychain, or in `0600` files in a `0700`
directory on Linux. No command prints a token.

The daemon **renews** each stored credential before it expires
(`refresh_window`, 1 hour), and renews idle ones every `refresh_probe` (24
hours) so a dead refresh token is found within a day, not when you need the
account. A dead credential shows as `needs login` or a 401; sign in again
with `cs login <id> --direct`.
[GUIDE → Keeping credentials alive](GUIDE.md#keeping-credentials-alive).

A swap never destroys a login. Before overwriting the live credential it
saves it to its account's entry, or, when it cannot tell whose it is, to a
**recovery copy**. `cs recovery` (and Settings → Accounts) lists, identifies,
restores and clears them.
[GUIDE → Recovery copies](GUIDE.md#recovery-copies).

## The call budget and 429s

The usage API locks an account out for 10 to 15 minutes after a burst of
about 24 calls, answering **429**. claudeswitch therefore spends calls from a
budget: **`api_budget`** (12) calls per 5 minutes across every process, with
one held back for swaps. The live account is read every **`poll_active`**
(3 minutes), or every **`poll_hot`** (1 minute) once it is above
**`hot_threshold`** (60%) and climbing; the others every **`poll_idle`**
(10 minutes). So is the live account of a profile where no session is
working: nothing there is spending it, and it is read again as soon as one
starts. A restarted daemon picks up from its last readings rather than
reading everything again. While the daemon runs, it is the only reader:
`cs status`, the status line and the app show its readings and spend nothing.

A 429 clears by itself. The daemon backs off 5 minutes, doubling; when the
API asks for a longer wait it waits longer each time it is refused again (up
to an hour for an idle account, 30 minutes for the one in use). Do not lower
`poll_hot` below its default.

The usage API also answers an **expired** access token with 429. No session
renews the token of a profile nobody is working in, so claudeswitch never
sends an expired token: the account shows "access token expired; parked" and
the daemon renews that profile's token itself (never a busy profile's, whose
session renews its own). If the renewal is refused, the account needs a
sign-in and the app says so.
`cs doctor` shows the budget in use.
[GUIDE → Advanced](GUIDE.md#advanced).

## Claude in Chrome routing

The Claude in Chrome extension keeps its own claude.ai login, one per Chrome
profile, and Claude Code reaches it only when both are on the same account.
claudeswitch cannot move that login, so it routes by Chrome profile. An
account uses:

1. its **own** Chrome profile, if it has one (`cs chrome add`);
2. else its **profile's** Chrome profile (`cs profile set <p> chrome`);
3. else **Chrome's last used**.

An account with its own Chrome profile, signed in as itself, follows every
rotation with no action. A Chrome profile shared by a profile's accounts
needs one sign-in after each rotation; the daemon, `cs use` and the app's
card say so, and `cs chrome signin <id>` opens the pages. claudeswitch never
writes Chrome's files and reads only the profile list in `Local State`.
[GUIDE → Claude in Chrome](GUIDE.md#claude-in-chrome).

[← Documentation index](README.md)
