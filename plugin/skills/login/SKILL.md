---
name: login
user-invocable: false
description: Sign in to a Claude account and store it in the claudeswitch vault, verifying it is the right account — for adding an account or renewing one whose credential died. Use when asked to "add an account", "log in to X", or when doctor/switch reports a credential that needs signing in again.
allowed-tools:
  - Bash(claudeswitch login:*)
  - Bash(claudeswitch config:*)
  - Bash(claudeswitch pool list:*)
  - AskUserQuestion
---

# claudeswitch login

Reached as `/cs login <id>` (and `/cs add`). This stores a credential in the
keychain and may add the account to the config, so **always confirm first**.

## 1. Confirm

The account does not need to be in the config yet. A name the config has never
seen is added by the login itself — an `[[account]]` block pinned to the seat
that actually signs in, last in priority — so never ask the user to write a
block first. A name already in the config is verified against its pin, and an
unpinned one gets pinned.

For a new account in a config with profiles, run `claudeswitch pool list`
first: it lists every pool, its accounts and the profiles that use it.

Ask with AskUserQuestion before going on, in one call:

- **which account id** — the one given after `/cs login`, or a short name for a
  new account (e.g. `work-b`, `personal`);
- **which browser** to sign in with. The login returns **whichever person the
  browser is signed in as**, so a browser the user is not normally signed into
  (Safari, Firefox, and so on) is how a different person is reached;
- **which pool** a new account goes into, when the config has profiles: each
  pool from `pool list` with the profiles that use it, the pool only this
  session's profile uses first (nothing is shared unless the user picks a
  shared pool), and "no pool" (the profile named `default` uses it last).
  Skip this for an account already in the config: it keeps its pool;
- **which organization**, only when the user mentions one, or the person is
  already stored in another organization: its id, or the one the browser is
  in.

Include an option to cancel.

## 2. Start the login

Always use `--direct`: it leaves the live session untouched.

```bash
claudeswitch login <id> --direct --browser "<Browser Name>"
```

For a new account, add the pool chosen: `--pool <pool>`, or `--no-pool`.

Add `--sso` for an SSO-backed organization. Without `--browser` it prints a URL
for the user to open themselves.

The organization is the browser's current one unless the sign-in asks for
another. An account already in the config asks for its own by itself. To add
a person in a second organization (the browser sits in the one already
vaulted), add `--org <organization id>`; without it a seat already vaulted
under another name is refused.

`--direct` touches no live credential, so it works the same whichever Claude
Code profile this session belongs to. Only where a **new** account goes
depends on what is passed: `--pool <pool>` for the pool chosen, `--no-pool`
for none, or `--profile <name>` for that profile's own pool (or the first it
lists). With none of them it is in no pool, which the profile named
`default` uses last. An account already in the config keeps its pool.

Two things that are not a login, for when the user asks:

- **sharing accounts between profiles.** An account is in one pool, and
  profiles that list the same pool share its accounts, one profile at a
  time. That is `/cs pool`, or in a terminal `claudeswitch pool create
  <pool>`, `claudeswitch pool move <id> --to <pool>` and
  `claudeswitch profile pools <profile> <pool>...` (the pools a profile
  uses, in order); in the app, Settings → Accounts and Settings → Profiles;
- **a separate Claude Code profile** (its own config dir and live
  credential): `claudeswitch profile create <name> --pool <id> --seed <id>`
  in a terminal, then `claudeswitch run <name>`. Seeding refuses an account
  live in any other profile.

## 3. Get the code

The browser shows a code after the user approves. Ask the user to paste it,
then finish:

```bash
claudeswitch login <id> --code <code>
```

(This session has no terminal to prompt in, so the code is pasted here. In a
terminal of their own, `claudeswitch login` asks for the name, the pool and the
browser and finishes when the browser comes back, with no code to paste.)

## 4. Report

On success, say it is vaulted, and pass on the config line it prints (`added
<id> to … pinned to seat …` or `pinned <id> …`). A running daemon picks the
change up by itself; no restart is needed.

If the output warns that no refresh token was issued, pass that on: the account
will need signing in again when the token expires.

Three refusals store nothing:

- **another organization** — the sign-in asked for an organization (`--org`,
  or the account's own) and came back in another: the browser is signed in
  as someone who is not in it. Suggest signing in as the right person, or
  another browser, and starting again;
- **a seat mismatch** — the login came back as a different account than the one
  that name is pinned to. Say which account actually came back and suggest
  retrying with a browser signed into the right one;
- **already vaulted under another name** — that account is already stored as
  the name in the message. Say so; the user does not need to add it twice. If
  they meant the same person in another organization, start again with
  `--org`.
