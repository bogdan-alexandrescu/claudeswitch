---
name: login
description: Sign in to a Claude account and store it in the claudeswitch vault, verifying it is the right account — for adding an account or renewing one whose credential died. Use when asked to "add an account", "log in to X", or when doctor/switch reports a credential that needs signing in again.
allowed-tools:
  - Bash(claudeswitch login:*)
  - Bash(claudeswitch config:*)
  - AskUserQuestion
---

# claudeswitch login

This stores a credential in the keychain, so **always confirm first**.

## 1. Confirm

The account id must already be in the config (`claudeswitch config` shows the
path); `login` refuses an id it does not know. If it is missing, tell the user
to add an `[[account]]` block or run `claudeswitch setup` in a terminal, and stop.

Ask with AskUserQuestion before going on: which account id, and which browser
to use. The login returns **whichever account the browser is signed into**, and
nothing in the request can override that, so a browser the user is not normally
signed into (Safari, Firefox, and so on) is how a different account is reached.
Include an option to cancel.

## 2. Start the login

Always use `--direct`: it leaves the live session untouched.

```bash
claudeswitch login <id> --direct --browser "<Browser Name>"
```

Add `--sso` for an SSO-backed organization. Without `--browser` it prints a URL
for the user to open themselves.

## 3. Get the code

The browser shows a code after the user approves. Ask the user to paste it,
then finish:

```bash
claudeswitch login <id> --code <code>
```

## 4. Report

It checks the credential against the account's seat and refuses a mismatch.
On success, say it is vaulted. If the output warns that no refresh token was
issued, pass that on: the account will need signing in again when the token
expires. On a mismatch, say which account actually came back and suggest
retrying with a browser signed into the right one.
