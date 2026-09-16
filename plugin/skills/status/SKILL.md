---
name: status
description: Show how much Claude quota every claudeswitch account has left — session (5-hour) and weekly windows, resets, and which account is active. Use when asked "how much quota is left", "am I near my limit", "which account am I on", or before starting a long task when usage may be high.
allowed-tools:
  - Bash(claudeswitch status:*)
  - Bash(claudeswitch whoami:*)
  - Bash(cs status:*)
---

# claudeswitch status

Run:

```bash
claudeswitch status
```

Add `--detail` when the user wants burn rate, reading age or which limit binds.

Summarise for the user in a few lines:

- the **active** account and its session and weekly utilization, with reset times
- any account marked refused/burnt, and when it recovers
- the account with the **most room** if the active one is at or past the
  switch threshold shown in the output

Do not paste the whole table back unless asked — the user can read it in the
tool output. If the command is not found, say claudeswitch is not installed and
point to `/claudeswitch:setup`.

If the user then wants to move accounts, that is `/claudeswitch:switch`.
