---
name: switch
description: Switch Claude Code to another claudeswitch account, hot and without a restart. Use when asked to "switch accounts", "move me to X", "use the account with the most room", or when the active account is at its limit.
allowed-tools:
  - Bash(claudeswitch why:*)
  - Bash(claudeswitch use:*)
  - Bash(claudeswitch statusline:*)
---

# claudeswitch switch

Switching is quick and reversible, so do it without asking for confirmation.

## 1. Pick the target

If the user named an account, use that id.

Otherwise run:

```bash
claudeswitch why --json
```

Use `decision.target` if the decision is `switch`. If not, pick the eligible
account (`"eligible": true`, not `"active"`) with the lowest `utilization`.
If none is eligible, do not switch: report which account recovers first
(`decision.recovers_account`, `decision.recovers_at`) and stop.

## 2. Swap

```bash
claudeswitch use <id>
```

It verifies the credential belongs to the expected account and rolls back if
not. Running sessions, this one included, pick up the new account from the next
request; nothing needs restarting.

## 3. Report

One line: from which account to which, with the new account's session and
weekly utilization from the output.

If `use` fails because the account is not in the vault, or its credential no
longer works, suggest `/claudeswitch:login <id>`. Do not start a login yourself.

A running daemon may rotate again on its own. A manual switch gets a cooldown,
so this is rare, but mention it if the user seems to expect the choice to stick
for good.
