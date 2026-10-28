# Claude in Chrome in the app

Where the app lets you choose Chrome profiles for Claude in Chrome, and what its sign-in banner means.

See also: [cs chrome](../cli/chrome.md) · [Concepts → Claude in Chrome routing](../concepts.md#claude-in-chrome-routing) · [GUIDE → Claude in Chrome](../GUIDE.md#claude-in-chrome)

The Claude in Chrome extension keeps its own claude.ai login, one per Chrome
profile. After a rotation Claude Code is on another account, and the browser
tools work only from a Chrome profile signed in as that account. An account
uses, in order: its own Chrome profile, its profile's, or Chrome's last used.

## A Chrome profile per profile

In [Settings → Profiles](settings-profiles.md), an open profile's **Claude in
Chrome → Chrome profile** picker lists your Chrome profiles by name (read from
Chrome's `Local State`). The choice applies to the profile's accounts that
have none of their own. Default: Chrome's last used, with its name in
brackets. CLI: `cs profile set <p> chrome <name>`, or `inherit`.

A Chrome profile shared this way needs one sign-in after each rotation.

## A Chrome profile per account

In [Settings → Accounts](settings-accounts.md), an account's ⋯ menu has
**Chrome Profile: …**:

| choice | what it does | CLI |
|---|---|---|
| **Same as Its Profile** | the account has none of its own (the default) | `cs chrome forget <id>` |
| one of **Your Chrome Profiles** | the account uses that Chrome profile | `cs chrome add <id> --existing <name>` |
| **Its Own (…)** | shown, checked, when claudeswitch made one for it | |
| **Create a New One** | makes a new Chrome profile for the account and opens it at the claude.ai sign-in and the extension's page | `cs chrome add <id>` |

An account with its own Chrome profile, signed in as that account, routes by
itself: browser tasks follow every rotation to it.

**This is the recommended setup** (IMPROVEMENTS P2): one Chrome profile per
account, each signed in once to claude.ai and to Claude in Chrome as its
account. claudeswitch cannot move the extension's login from one account to
another; with a profile per account it never has to. `cs setup` offers to
make one for each account at the end, and the sign-in banner below has
**Give \<account\> Its Own Chrome Profile** for the account it names.

## Opening Chrome

- The globe on a popover card opens the live account's Chrome profile.
- **Open Chrome** in Settings → Profiles and in an account's ⋯ menu does the
  same for that profile's live account, or that account.

Each is greyed out while it opens, so a double click opens one window.

CLI: `cs chrome open <id>`.

## The Chrome banner

<p align="center"><img src="../images/popover-chrome-signin.png" width="384" alt="The popover with the work card open: Claude in Chrome in &quot;Work&quot; does not follow switches to work-team, with a Give work-team Its Own Chrome Profile button and a close button"></p>

An account with a Chrome profile of its own needs nothing here: after every
switch to it, claudeswitch opens that profile in the background and Claude in
Chrome follows (C4). When a profile's accounts share a Chrome profile and
rotation moved the profile to another account, the shared profile cannot
follow, and its card says so. **Give work-team Its Own Chrome Profile**
creates one and opens it at the claude.ai and Claude in Chrome sign-in pages
(`cs chrome add work-team`): sign in to both as work-team, once. It never asks
for a sign-in after a rotation. The banner goes once that worked; if it
failed, the banner stays and the popover says why. × hides the banner
until the next rotation.

[Settings → Overview](settings-overview.md) lists the same notice under
**Needs you**, with the same button.

When Chrome is not supported on the machine, the globe and the Chrome
controls are hidden or greyed out.

## Claude Code's own login

Claude in Chrome also checks the other end: the account Claude Code says it
is logged in as, which it records in the profile's `.claude.json`. A swap
used to change Claude Code's credential but not that record, and the bridge
then refused with "the OAuth token Claude Code is using belongs to a
different claude.ai account than the one Claude Code is logged in as".

Every swap now rewrites the record from one profile read with the new
account's token, and the daemon puts a stray one right on its next poll
(GROUND_TRUTH §53). Until it does, the status line ends in
`· login <name> ≠`, `cs doctor` and `cs why` say so, and
`cs whoami --sync` lines them up at once. The audit log records each change
as a `login` event.

[← Documentation index](../README.md)
