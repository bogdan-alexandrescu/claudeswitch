---
name: login
user-invocable: false
description: Sign in to a Claude account and store it in the claudeswitch vault, verifying it is the right account — for adding an account or renewing one whose credential died. Use when asked to "add an account", "log in to X", or when doctor/switch reports a credential that needs signing in again.
allowed-tools:
  - Bash(claudeswitch login:*)
  - Bash(claudeswitch config:*)
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

Ask with AskUserQuestion before going on, in one call:

- **which account id** — the one given after `/cs login`, or a short name for a
  new account (e.g. `work-b`, `personal`);
- **which browser** to sign in with. The login returns **whichever account the
  browser is signed into**, and nothing in the request can override that, so a
  browser the user is not normally signed into (Safari, Firefox, and so on) is
  how a different account is reached;
- for a new account only, **work or personal** scope (default work).

Include an option to cancel.

## 2. Start the login

Always use `--direct`: it leaves the live session untouched.

```bash
claudeswitch login <id> --direct --browser "<Browser Name>" [--scope personal]
```

Add `--sso` for an SSO-backed organization. Without `--browser` it prints a URL
for the user to open themselves.

`--direct` touches no live credential, so it works the same whichever Claude
Code profile this session belongs to; it needs no `--profile`.

## 3. Get the code

The browser shows a code after the user approves. Ask the user to paste it,
then finish:

```bash
claudeswitch login <id> --code <code>
```

## 4. Report

On success, say it is vaulted, and pass on the config line it prints (`added
<id> to … pinned to seat …` or `pinned <id> …`). A running daemon picks the
change up by itself; no restart is needed.

If the output warns that no refresh token was issued, pass that on: the account
will need signing in again when the token expires.

Two refusals store nothing:

- **a seat mismatch** — the login came back as a different account than the one
  that name is pinned to. Say which account actually came back and suggest
  retrying with a browser signed into the right one;
- **already vaulted under another name** — that account is already stored as
  the name in the message. Say so; the user does not need to add it twice.
