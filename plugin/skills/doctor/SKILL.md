---
name: doctor
description: Diagnose a claudeswitch installation — config, vault, keychain access, usage API, daemon, refresh policy, status line and plugin — and explain how to fix what fails. Use when claudeswitch misbehaves, a command errors, or accounts are not polling.
allowed-tools:
  - Bash(claudeswitch doctor:*)
  - Bash(claudeswitch version:*)
  - Bash(tail:*)
---

# claudeswitch doctor

```bash
claudeswitch version
claudeswitch doctor
```

`doctor` reads the live credential, so macOS may show a keychain prompt the
first time a newly built binary runs it. That is expected: tell the user to
choose **Always Allow**.

Add `--verify` only if the user wants every vaulted credential checked — it
makes one API call per account.

For each `FAIL` or `warn` line, say what it means and give the exact fix the
output suggests. If the daemon looks stuck, its log is at
`~/.local/state/claudeswitch/daemon.log`; read the last lines with `tail -50`.

Do not run `login`, `remove` or `forget` from here. Suggest
`/claudeswitch:login` when an account needs signing in again.
