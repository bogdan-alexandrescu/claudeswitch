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
**Chrome profile: …**:

| choice | what it does | CLI |
|---|---|---|
| **Same as its profile** | the account has none of its own (the default) | `cs chrome forget <id>` |
| one of **Your Chrome profiles** | the account uses that Chrome profile | `cs chrome add <id> --existing <name>` |
| **Its own (…)** | shown, checked, when claudeswitch made one for it | |
| **Create a new one** | makes a new Chrome profile for the account and opens it at the claude.ai sign-in and the extension's page | `cs chrome add <id>` |

An account with its own Chrome profile, signed in as that account, routes by
itself: browser tasks follow every rotation to it.

## Opening Chrome

- The globe on a popover card opens the live account's Chrome profile.
- **Open Chrome** in Settings → Profiles and in an account's ⋯ menu does the
  same for that profile's live account, or that account.

CLI: `cs chrome open <id>`.

## The sign-in banner

<p align="center"><img src="../images/popover-chrome-signin.png" width="384" alt="The work card: Claude in Chrome in &quot;Work&quot; is still signed in as work-1, with a Sign in as work-team button and a close button"></p>

When a profile's accounts share a Chrome profile and rotation moved the
profile to another account, its card says which Chrome profile is still
signed in as the old account. **Sign in as work-team** opens that Chrome
profile at the claude.ai and Claude in Chrome sign-in pages
(`cs chrome signin work-team`): sign in to both as work-team. × hides the
banner until the next rotation.

When Chrome is not supported on the machine, the globe and the Chrome
controls are hidden or greyed out.

[← Documentation index](../README.md)
