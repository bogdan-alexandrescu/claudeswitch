---
name: switch
user-invocable: false
description: Switch Claude Code to another claudeswitch account, hot and without a restart. Use when asked to "switch accounts", "move me to X", "use the account with the most room", or when the active account is at its limit.
allowed-tools:
  - Bash(claudeswitch why:*)
  - Bash(claudeswitch use:*)
  - Bash(claudeswitch statusline:*)
  - Bash(claudeswitch chrome:*)
---

# claudeswitch switch

Switching is quick and reversible, so do it without asking for confirmation.

## 1. Pick the target

If the user named an account, use that id.

Otherwise run:

```bash
claudeswitch why --json
```

With several Claude Code profiles configured, the output is a `profiles`
list instead, one entry per profile; read only the entry with
`"current": true` — this session's profile, the one `use` acts on — and
ignore the others. Each profile rotates only within its own pool.

Use `decision.target` if the decision is `switch`. If not, pick the eligible
account (`"eligible": true`, not `"active"`) with the lowest `utilization`.
If none is eligible, do not switch: report which account recovers first
(`decision.recovers_account`, `decision.recovers_at`) and stop.

## 2. Swap

```bash
claudeswitch use <id>
```

With no `--profile`, `use` switches the profile this session runs in (the
one its `CLAUDE_CONFIG_DIR` belongs to). Pass `--profile <name>` only when the
user asks to switch a different profile by name.

It verifies the credential belongs to the expected account and rolls back if
not. Running sessions, this one included, pick up the new account from the next
request; nothing needs restarting.

`use` refuses an account from another profile's pool, naming the profile that
owns it, and refuses one that is (or may be) live in another profile. Do not
try to work around either: report the refusal, and suggest `/cs switch <id>`
from a session of the owning profile, or `--profile <owner>` if the user
wants that profile switched instead.

## 3. Report

One line: from which account to which, with the new account's session and
weekly utilization from the output.

If the output has a `Claude in Chrome: use the <id> Chrome profile` line,
repeat it: Claude in Chrome answers only when the extension is signed in to
the same claude.ai account as Claude Code, and the user keeps one Chrome
profile per account. `claudeswitch chrome <id>` opens it; run it only if the
user asks.

If `use` fails because the account is not in the vault, or its credential no
longer works, suggest `/cs login <id>`. Do not start a login yourself.

A running daemon may rotate again on its own. A manual switch gets a cooldown,
so this is rare, but mention it if the user seems to expect the choice to stick
for good.
